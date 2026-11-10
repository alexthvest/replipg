package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/config"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type reviewInput struct {
	CatalogID string `json:"catalog_id,omitempty"`
	FixtureID string `json:"fixture_id,omitempty"`
	From      string `json:"from,omitempty"`
	Quantity  int64  `json:"quantity"`
}

type reviewRecipe struct {
	Name   string        `json:"name"`
	Title  string        `json:"title"`
	Inputs []reviewInput `json:"inputs"`
}

// TestCraftingReview is a manual experiment. It captures current assets and
// reuses complete generated intermediates without opening a gameplay database.
func TestCraftingReview(t *testing.T) {
	if os.Getenv("REPLIPG_CRAFTING_REVIEW") != "true" {
		t.Skip("set REPLIPG_CRAFTING_REVIEW=true for local review generation")
	}
	root := "../../../openspec/changes/archive/2026-11-10-add-ai-crafting/experiments/current-review"
	if path := os.Getenv("REPLIPG_CRAFTING_REVIEW_DIR"); path != "" {
		root = path
	}
	data, err := os.ReadFile(filepath.Join(root, "recipes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recipes []reviewRecipe
	if err = strictJSON(data, &recipes); err != nil || len(recipes) == 0 {
		t.Fatalf("read review recipes: %v", err)
	}
	seen := map[string]bool{}
	for _, recipe := range recipes {
		if recipe.Name == "" || seen[recipe.Name] || len(recipe.Inputs) == 0 {
			t.Fatal("unique nonempty recipes required")
		}
		for _, input := range recipe.Inputs {
			sources := 0
			for _, source := range []string{input.CatalogID, input.FixtureID, input.From} {
				if source != "" {
					sources++
				}
			}
			if input.Quantity <= 0 || sources != 1 || (input.From != "" && !seen[input.From]) {
				t.Fatalf("invalid or forward input reference in %s", recipe.Name)
			}
		}
		seen[recipe.Name] = true
	}
	assets, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]items.Snapshot{}
	fixtureData, fixtureErr := os.ReadFile(filepath.Join(root, "materials.json"))
	if fixtureErr != nil && !os.IsNotExist(fixtureErr) {
		t.Fatal(fixtureErr)
	}
	if fixtureErr == nil {
		if err = strictJSON(fixtureData, &fixtures); err != nil {
			t.Fatal(err)
		}
		for id, snapshot := range fixtures {
			if snapshot.Validate() != nil || !snapshot.Components.Material {
				t.Fatalf("invalid material fixture %s", id)
			}
		}
	}
	opts := config.Crafting{
		Endpoint:         os.Getenv("REPLIPG_CRAFTING_ENDPOINT"),
		Model:            os.Getenv("REPLIPG_CRAFTING_MODEL"),
		Token:            os.Getenv("REPLIPG_CRAFTING_TOKEN"),
		Timeout:          2 * time.Minute,
		MinInterval:      time.Second,
		MaxResponseBytes: 65536,
		MaxTokens:        2048,
	}
	paths, err := filepath.Glob(filepath.Join(root, "prompts", "*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatal("review prompt files required")
	}
	outputPath := os.Getenv("REPLIPG_CRAFTING_REVIEW_OUTPUT")
	if outputPath == "" {
		outputPath = filepath.Join(root, "results.jsonl")
	}
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	encoder := json.NewEncoder(output)
	for _, path := range paths {
		prompt, readErr := os.ReadFile(path)
		if readErr != nil || strings.TrimSpace(string(prompt)) == "" {
			t.Fatalf("read prompt %s: %v", path, readErr)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".txt")
		for _, seed := range []int{17, 42} {
			t.Run(fmt.Sprintf("%s/%d", name, seed), func(t *testing.T) {
				generator, newErr := New(opts)
				if newErr != nil {
					t.Fatal(newErr)
				}
				transport := generator.client.Transport.(*http.Transport)
				t.Cleanup(transport.CloseIdleConnections)
				var lastContent string
				generator.client.Transport = reviewTransport{
					base:    transport,
					prompt:  string(prompt),
					seed:    seed,
					content: &lastContent,
				}
				generated := map[string]items.Snapshot{}
				for _, recipe := range recipes {
					t.Run(recipe.Name, func(t *testing.T) {
						var allocations []domain.Allocation
						var inputErr error
						for _, input := range recipe.Inputs {
							var snapshot items.Snapshot
							if input.From != "" {
								var ok bool
								if snapshot, ok = generated[input.From]; !ok || !snapshot.Components.Material {
									inputErr = fmt.Errorf("source %s is unavailable or not craftable", input.From)
									break
								}
							} else if input.FixtureID != "" {
								var ok bool
								if snapshot, ok = fixtures[input.FixtureID]; !ok {
									inputErr = fmt.Errorf("unknown material fixture %s", input.FixtureID)
									break
								}
							} else {
								snapshot, inputErr = assets.Capture(input.CatalogID)
								if inputErr != nil {
									break
								}
							}
							allocations = append(allocations, domain.Allocation{
								Snapshot: snapshot,
								Quantity: input.Quantity,
							})
						}
						identity, identifyErr := domain.Identify(allocations)
						if inputErr == nil {
							inputErr = identifyErr
						}
						start := time.Now()
						var outcome domain.Outcome
						generateErr := inputErr
						lastContent = ""
						if inputErr == nil {
							outcome, generateErr = generator.Generate(t.Context(), identity.Ingredients)
						}
						record := map[string]any{
							"recipe":      recipe.Name,
							"title":       recipe.Title,
							"inputs":      recipe.Inputs,
							"ingredients": identity.Ingredients,
							"prompt":      name,
							"seed":        seed,
							"model":       opts.Model,
							"outcome":     outcome,
							"duration_ms": time.Since(start).Milliseconds(),
						}
						if generateErr != nil {
							record["error"] = generateErr.Error()
							record["blocked"] = inputErr != nil
							if lastContent != "" {
								record["rejected_content"] = lastContent
							}
						}
						if encodeErr := encoder.Encode(record); encodeErr != nil {
							t.Fatal(encodeErr)
						}
						if generateErr != nil {
							t.Fatal(generateErr)
						}
						generated[recipe.Name] = *outcome.Item
						t.Logf("%s — %s", outcome.Item.Russian.Name, outcome.Item.Russian.Description)
					})
				}
			})
		}
	}
}

type reviewTransport struct {
	base    http.RoundTripper
	prompt  string
	seed    int
	content *string
}

func (r reviewTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body struct {
		Model    string          `json:"model"`
		Messages []message       `json:"messages"`
		Stream   bool            `json:"stream"`
		Think    bool            `json:"think"`
		Format   json.RawMessage `json:"format"`
		Options  map[string]any  `json:"options"`
	}
	err := json.NewDecoder(request.Body).Decode(&body)
	_ = request.Body.Close()
	if err != nil {
		return nil, err
	}
	body.Messages[0].Content = r.prompt
	body.Options["seed"] = r.seed
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	copy := request.Clone(request.Context())
	copy.Body = io.NopCloser(bytes.NewReader(raw))
	copy.ContentLength = int64(len(raw))
	response, err := r.base.RoundTrip(copy)
	if err != nil {
		return nil, err
	}
	rawResponse, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	_ = response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	response.Body = io.NopCloser(bytes.NewReader(rawResponse))
	var envelope struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(rawResponse, &envelope) == nil {
		*r.content = envelope.Message.Content
	}
	return response, nil
}

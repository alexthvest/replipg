// Command probe checks an operational endpoint without printing its body.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	os.Exit(probe(os.Args[1:]))
}

func probe(args []string) int {
	endpoint := "readyz"
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: scripts/probe [livez|readyz|statusz]")
		return 2
	}

	if len(args) == 1 {
		endpoint = args[0]
	}
	switch endpoint {
	case "livez", "readyz", "statusz":
	default:
		fmt.Fprintln(os.Stderr, "usage: scripts/probe [livez|readyz|statusz]")
		return 2
	}

	address := os.Getenv("REPLIPG_HEALTH_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration setting REPLIPG_HEALTH_ADDRESS")
		return 2
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	response, err := client.Get("http://" + address + "/" + endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe unavailable")
		return 1
	}
	defer func() { _ = response.Body.Close() }()

	fmt.Printf("%s: HTTP %d\n", endpoint, response.StatusCode)
	if response.StatusCode != http.StatusOK {
		return 1
	}

	return 0
}

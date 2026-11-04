package settings

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type handler struct {
	store      *Store
	catalog    *localization.Catalog
	membership runtimecontract.GroupMembershipReader
	now        func() time.Time
}

func Register(r *interaction.Registry, s *Store, c *localization.Catalog, m runtimecontract.GroupMembershipReader, now func() time.Time) error {
	if r == nil || s == nil || s.menu == nil || c == nil || m == nil || now == nil {
		return ErrInvalid
	}
	h := handler{s, c, m, now}
	if err := r.RegisterCommand("settings", h); err != nil {
		return err
	}
	return r.RegisterCallback("set:", h)
}

type request struct {
	identity   interaction.Identity
	data, args string
	query      interaction.CallbackQueryID
	message    interaction.EphemeralMessageID
	deadline   time.Time
	callback   bool
}

func requestFor(event interaction.Event, now time.Time) (request, bool) {
	switch e := event.(type) {
	case *interaction.Command:
		if e != nil {
			return requestFor(*e, now)
		}
	case *interaction.Callback:
		if e != nil {
			return requestFor(*e, now)
		}
	case interaction.Command:
		if e.Name != "settings" || e.ActorIsBot || e.SenderIsChat || e.SentAt.IsZero() || !now.Before(e.SentAt.Add(15*time.Second)) {
			return request{}, false
		}
		return validRequest(request{identity: e.Identity, args: e.Arguments, message: e.EphemeralMessageID, deadline: e.SentAt.Add(15 * time.Second)})
	case interaction.Callback:
		if e.ActorIsBot || e.QueryID == "" || !strings.HasPrefix(e.Data, "set:") {
			return request{}, false
		}
		return validRequest(request{identity: e.Identity, data: e.Data, query: e.QueryID, message: e.EphemeralMessageID, deadline: now.Add(15 * time.Second), callback: true})
	}
	return request{}, false
}
func validRequest(r request) (request, bool) {
	return r, r.identity.Update > 0 && r.identity.User > 0 && r.identity.Chat < 0 && (r.identity.ChatKind == runtimecontract.ChatKindGroup || r.identity.ChatKind == runtimecontract.ChatKindSupergroup) && r.message > 0 && int64(int(r.message)) == int64(r.message)
}

type prepared struct {
	handler handler
	request request
	status  runtimecontract.MembershipStatus
	checked time.Time
	valid   bool
}

func (h handler) Prepare(ctx context.Context, event interaction.Event) (interaction.Handler, error) {
	r, ok := requestFor(event, h.now())
	p := prepared{handler: h, request: r, valid: ok}
	if !ok {
		return p, nil
	}
	result, err := h.membership.VerifyGroupMember(ctx, r.identity.Chat, r.identity.User)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil {
		p.status = result.Status
		p.checked = result.CheckedAt
	}
	return p, nil
}
func (h handler) Handle(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
	return interaction.HandlerResult{}, errors.New("settings requires prepared membership")
}
func (p prepared) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	h := p.handler
	now := h.now()
	r, ok := requestFor(event, now)
	if !p.valid || !ok || r.identity.Update != p.request.identity.Update || r.identity.Chat != p.request.identity.Chat || r.identity.User != p.request.identity.User || r.data != p.request.data || r.args != p.request.args || r.query != p.request.query || r.message != p.request.message || r.callback != p.request.callback || !now.Before(p.request.deadline) {
		return interaction.HandlerResult{}, nil
	}
	r.deadline = p.request.deadline
	inspect := h.store.Inspect
	if r.callback {
		inspect = h.store.view
	}
	v, err := inspect(ctx, scope.DBTX(), r.identity.Chat, now)
	if err != nil {
		return interaction.HandlerResult{}, err
	}
	now = h.now()
	if !now.Before(r.deadline) {
		return interaction.HandlerResult{}, nil
	}
	fresh := !p.checked.IsZero() && !p.checked.After(now) && now.Before(p.checked.Add(5*time.Second))
	admin := fresh && (p.status == runtimecontract.Administrator || p.status == runtimecontract.Owner)
	guidance := ""
	if !admin && fresh && p.status == runtimecontract.Member {
		guidance = "settings.read_only"
	}
	if r.callback {
		if !admin {
			guidance = "settings.unavailable"
			if !fresh && !p.checked.IsZero() {
				guidance = "settings.reopen"
			}
			if fresh && p.status == runtimecontract.Member {
				guidance = "settings.read_only"
			}
		} else {
			v, err = h.store.applyControl(ctx, scope.DBTX(), r.identity.Chat, r.identity.User, r.data, now, func() (time.Time, bool) {
				at := h.now()
				return at, !p.checked.IsZero() && !p.checked.After(at) && at.Before(p.checked.Add(5*time.Second)) && at.Before(r.deadline)
			})
			if errors.Is(err, ErrStale) || errors.Is(err, ErrEvidenceExpired) {
				admin = false
				v, err = inspect(ctx, scope.DBTX(), r.identity.Chat, now)
				guidance = "settings.reopen"
			} else if err == nil {
				guidance = "settings.saved"
			}
			if err != nil {
				return interaction.HandlerResult{}, err
			}
		}
	} else if strings.TrimSpace(r.args) != "" {
		guidance = "settings.usage"
	}
	content, err := h.render(ctx, scope.DBTX(), v, r.identity.User, admin, guidance, now)
	if err != nil {
		return interaction.HandlerResult{}, err
	}
	var auth runtimecontract.EphemeralAuthorization = runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(r.message), ValidUntil: r.deadline}
	edit := 0
	if r.callback {
		auth = runtimecontract.CallbackAuthorization{CallbackQueryID: string(r.query), OriginatedFromEphemeral: true, ValidUntil: r.deadline}
		edit = int(r.message)
	}
	response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: r.identity.Chat, Kind: r.identity.ChatKind}, TargetUserID: r.identity.User, Authorization: auth, Content: content, EditMessageID: edit}
	if err := runtimecontract.ValidateResponse(response); err != nil {
		return interaction.HandlerResult{}, err
	}
	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response}}, nil
}

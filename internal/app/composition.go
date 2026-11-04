package app

import (
	"context"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
	"github.com/alexthvest/replipg/internal/telegram"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type workScope struct {
	tx         runtimecontract.DBTX
	deliveries *outbox.Store
	jobs       *scheduler.Service
}

func (s workScope) DBTX() runtimecontract.DBTX { return s.tx }

func (s workScope) EnqueueDelivery(ctx context.Context, req runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	return s.deliveries.Enqueue(ctx, s.tx, req)
}

func (s workScope) Schedule(ctx context.Context, req runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	return s.jobs.Schedule(ctx, s.tx, req)
}

type deliveryPort struct{ sender runtimecontract.Sender }

func (d deliveryPort) Deliver(ctx context.Context, request runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	switch req := request.(type) {
	case runtimecontract.PublicResponse:
		receipt, err := d.sender.SendPublic(ctx, req)
		return telegram.Outcome(receipt, err)
	case *runtimecontract.PublicResponse:
		if req != nil {
			return d.Deliver(ctx, *req)
		}
	case runtimecontract.EphemeralResponse:
		receipt, err := d.sender.SendEphemeral(ctx, req)
		return telegram.Outcome(receipt, err)
	case *runtimecontract.EphemeralResponse:
		if req != nil {
			return d.Deliver(ctx, *req)
		}
	}

	return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure, ReasonCode: "invalid_request"}
}

// boundedRouter keeps handler cancellation within the transaction boundary;
// delivery after a durable commit retains the caller's work context.
type boundedRouter struct {
	telegram.UpdateRouter
	timeout time.Duration
}

func (r boundedRouter) Dispatch(ctx context.Context, event interaction.Event) (interaction.DispatchOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.UpdateRouter.Dispatch(ctx, event)
}

package outbox

import "github.com/alexthvest/replipg/internal/runtimecontract"

type routingMetadata struct {
	chat          runtimecontract.ChatID
	target        any
	authorization any
	callback      any
	incoming      any
	expires       any
	replace       bool
}

func responseRouting(response runtimecontract.ResponseRequest) (routingMetadata, error) {
	switch response := response.(type) {
	case runtimecontract.PublicResponse:
		return routingMetadata{chat: response.Destination.ChatID}, nil
	case *runtimecontract.PublicResponse:
		return routingMetadata{chat: response.Destination.ChatID}, nil
	case runtimecontract.EphemeralResponse:
		return ephemeralRouting(response), nil
	case *runtimecontract.EphemeralResponse:
		return ephemeralRouting(*response), nil
	default:
		return routingMetadata{}, invalid("delivery.response", runtimecontract.ReasonUnsupported)
	}
}

// All authorization cases have already passed ValidateResponseAt.
func ephemeralRouting(response runtimecontract.EphemeralResponse) routingMetadata {
	metadata := routingMetadata{chat: response.Destination.ChatID, target: response.TargetUserID, replace: response.ReplaceCallbackMessage}
	switch authorization := response.Authorization.(type) {
	case runtimecontract.CallbackAuthorization:
		metadata.authorization, metadata.callback, metadata.expires = "callback", authorization.CallbackQueryID, authorization.ValidUntil.UnixMilli()
	case *runtimecontract.CallbackAuthorization:
		metadata.authorization, metadata.callback, metadata.expires = "callback", authorization.CallbackQueryID, authorization.ValidUntil.UnixMilli()
	case runtimecontract.IncomingEphemeralAuthorization:
		metadata.authorization, metadata.incoming, metadata.expires = "incoming_ephemeral", authorization.EphemeralMessageID, authorization.ValidUntil.UnixMilli()
	case *runtimecontract.IncomingEphemeralAuthorization:
		metadata.authorization, metadata.incoming, metadata.expires = "incoming_ephemeral", authorization.EphemeralMessageID, authorization.ValidUntil.UnixMilli()
	case runtimecontract.AdministratorAuthorization, *runtimecontract.AdministratorAuthorization:
		metadata.authorization = "administrator"
	}
	return metadata
}

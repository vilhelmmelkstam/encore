package gcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"encore.dev/beta/errs"
	"encore.dev/pubsub/internal/types"
	"encore.dev/runtime"
	"encore.dev/runtime/config"
)

// This is documented in https://cloud.google.com/pubsub/docs/push
type pushPayload struct {
	Message struct {
		Attributes      map[string]string `json:"attributes"`
		Data            []byte            `json:"data"`
		MessageID       string            `json:"messageId"`
		PublishTime     time.Time         `json:"publishTime"`
		DeliveryAttempt int               `json:"deliveryAttempt,omitempty"` // Field documented in: https://cloud.google.com/pubsub/docs/handling-failures#track_delivery_attempts
	} `json:"message"`
	Subscription string `json:"subscription"`
}

func registerPushEndpoint(serverCfg *config.GCPPubSubServer, subscriptionConfig *config.PubsubSubscription, f types.RawSubscriptionCallback) {
	runtime.RegisterPubSubSubscriptionHandler(
		subscriptionConfig.ResourceID,
		func(req *http.Request) error {
			// If the request has not come from the Encore platform it must have
			// a valid JWT set by Google.
			if !runtime.IsEncoreAuthenticatedRequest(req.Context()) {
				if err := validateGoogleJWT(req, serverCfg); err != nil {
					return errs.Wrap(err, "unable to validate JWT")
				}
			}

			// Decode the payload
			payload := &pushPayload{}
			if err := json.NewDecoder(req.Body).Decode(payload); err != nil {
				return errs.WrapCode(err, errs.InvalidArgument, "invalid push payload")
			}

			// Call the subscription callback
			return f(
				req.Context(),
				payload.Message.MessageID, payload.Message.PublishTime, payload.Message.DeliveryAttempt,
				payload.Message.Attributes, payload.Message.Data,
			)
		},
	)
}

func validateGoogleJWT(req *http.Request, cfg *config.GCPPubSubServer) error {
	// Extract the JWT from the header
	authType, token, _ := strings.Cut(req.Header.Get("Authorization"), " ")
	if authType != "Bearer" {
		return errs.B().Code(errs.InvalidArgument).Msg("invalid auth header").Err()
	}

	// Validate it
	jwt, err := idtoken.Validate(req.Context(), token, config.Cfg.Runtime.AppID+"-"+config.Cfg.Runtime.EnvID)
	if err != nil {
		return errs.B().Code(errs.InvalidArgument).Msg("unable to validate authorization").Err()
	}
	if jwt.Issuer != "accounts.google.com" && jwt.Issuer != "https://accounts.google.com" {
		return errs.B().Code(errs.InvalidArgument).Msg("invalid issuer").Err()
	}
	if jwt.Claims["email"] != cfg.PushServiceAccount || jwt.Claims["email_verified"] != true {
		return errs.B().Code(errs.Unauthenticated).Meta("expected_email", cfg.PushServiceAccount, "got_email", jwt.Claims["email"]).Msg("invalid email").Err()
	}

	return nil
}

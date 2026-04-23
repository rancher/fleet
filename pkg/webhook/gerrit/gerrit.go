package gerrit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var (
	ErrEventNotSpecifiedToParse = errors.New("no Event specified to parse")
	ErrInvalidHTTPMethod = errors.New("invalid HTTP Method")
	ErrMissingGerritEvent = errors.New("missing type field")
	ErrMissingHubSignatureHeader = errors.New("missing X-Hub-Signature-256 Header")
	ErrEventNotFound = errors.New("event not defined to be parsed")
	ErrParsingPayload = errors.New("error parsing payload")
	ErrHMACVerificationFailed = errors.New("HMAC verification failed")
)

type Event string

const (
	ChangeMergedEvent Event = "change-merged"
)

// Option is a configuration option for the webhook
type Option func(*Webhook) error

// Options is a namespace var for configuration options
var Options = WebhookOptions{}

// WebhookOptions is a namespace for configuration option methods
type WebhookOptions struct{}

// GerritWebhook instance contains all methods needed to process events
type Webhook struct {}

// New creates and returns a GerritWebhook instance
func New(options ...Option) (*Webhook, error) {
	hook := new(Webhook)
	for _, opt := range options {
		if err := opt(hook); err != nil {
			return nil, errors.New("error applying option")
		}
	}
	return hook, nil
}
func (hook *Webhook) Parse(r *http.Request, events ...Event) (interface{}, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}()

	if len(events) == 0 {
		return nil, ErrEventNotSpecifiedToParse
	}
	if r.Method != http.MethodPost {
		return nil, ErrInvalidHTTPMethod
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil || len(payload) == 0 {
		return nil, ErrParsingPayload
	}

	var envelope Envelope
	err = json.Unmarshal([]byte(payload), &envelope)

	if err != nil {
		return nil, ErrParsingPayload
	}

	event := envelope.Type

	if event == "" {
		return nil, ErrMissingGerritEvent
	}

	gerritEvent := Event(event)

	var found bool
	for _, evt := range events {
		if evt == gerritEvent {
			found = true
			break
		}
	}

	// event not defined to be parsed
	if !found {
		return nil, ErrEventNotFound
	}

	switch gerritEvent {
	case ChangeMergedEvent:
		var pl ChangeMergedPayload
		err = json.Unmarshal([]byte(payload), &pl)
		return pl, err
	default:
		return nil, fmt.Errorf("unknown event %s", gerritEvent)
	}

}

func ExtractRepoURL(payload ChangeMergedPayload) (string, error) {
	// URL is in the format of https://<gerrit-host>/c/<project>/+/<change-id>
	parts := strings.Split(payload.Change.URL, "/")
	if len(parts) < 7 {
		return "", fmt.Errorf("invalid URL %s", payload.Change.URL)
	}
	
	url := fmt.Sprintf("%s//%s/%s", parts[0], parts[2], parts[4])
	return url, nil
}
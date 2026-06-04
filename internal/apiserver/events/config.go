package events

import (
	"time"

	"k8s.io/client-go/rest"
)

// Config holds connection parameters for the Activity API server proxy
type Config struct {
	BaseConfig *rest.Config

	ProviderURL string

	CAFile         string
	ClientCertFile string
	ClientKeyFile  string

	Timeout     time.Duration
	Retries     int
	ExtrasAllow map[string]struct{}
}

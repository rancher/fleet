package controllers

import (
	"context"

	"github.com/go-logr/logr"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	ctrlScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(ctrlScheme))
	utilruntime.Must(fleet.AddToScheme(ctrlScheme))

	// Set up controller-runtime logger using logrus
	log.SetLogger(NewLogrusLogger())
}

// logrusLogger is a simple adapter that implements logr.LogSink for logrus
type logrusLogger struct {
	level int
	name  string
}

// NewLogrusLogger creates a logr.Logger that uses logrus as the backend
func NewLogrusLogger() logr.Logger {
	return logr.New(&logrusLogger{})
}

func (l *logrusLogger) Init(info logr.RuntimeInfo) {}

func (l *logrusLogger) Enabled(level int) bool {
	return true
}

func (l *logrusLogger) Info(level int, msg string, keysAndValues ...interface{}) {
	entry := logrus.WithFields(l.fieldsFromKeysAndValues(keysAndValues))
	if l.name != "" {
		entry = entry.WithField("controller", l.name)
	}
	entry.Info(msg)
}

func (l *logrusLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	entry := logrus.WithFields(l.fieldsFromKeysAndValues(keysAndValues))
	if l.name != "" {
		entry = entry.WithField("controller", l.name)
	}
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Error(msg)
}

func (l *logrusLogger) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return &logrusLogger{
		level: l.level,
		name:  l.name,
	}
}

func (l *logrusLogger) WithName(name string) logr.LogSink {
	newName := name
	if l.name != "" {
		newName = l.name + "." + name
	}
	return &logrusLogger{
		level: l.level,
		name:  newName,
	}
}

func (l *logrusLogger) fieldsFromKeysAndValues(keysAndValues []interface{}) logrus.Fields {
	fields := logrus.Fields{}
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := keysAndValues[i].(string)
			fields[key] = keysAndValues[i+1]
		}
	}
	return fields
}

// ControllerRuntimeManager holds the controller-runtime manager
// This will be used alongside the wrangler-based controllers during migration
type ControllerRuntimeManager struct {
	mgr    ctrl.Manager
	client client.Client
}

// NewControllerRuntimeManager creates a new controller-runtime manager
// that will coexist with the wrangler-based controllers
func NewControllerRuntimeManager(cfg *rest.Config, namespace string) (*ControllerRuntimeManager, error) {
	// Create the manager with similar configuration to wrangler
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: ctrlScheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics for now to avoid conflicts
		},
		HealthProbeBindAddress: "0",   // Disable health probes for now
		LeaderElection:         false, // Leadership is handled by wrangler's leader election
		// Watch only the specific namespace if needed, or leave empty for all namespaces
		// Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, err
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, err
	}

	return &ControllerRuntimeManager{
		mgr:    mgr,
		client: mgr.GetClient(),
	}, nil
}

// GetManager returns the controller-runtime manager
func (m *ControllerRuntimeManager) GetManager() ctrl.Manager {
	return m.mgr
}

// GetClient returns the controller-runtime client
func (m *ControllerRuntimeManager) GetClient() client.Client {
	return m.client
}

// Start starts the controller-runtime manager
// This should be called after all controllers are registered
func (m *ControllerRuntimeManager) Start(ctx context.Context) error {
	logrus.Info("Starting controller-runtime manager")
	return m.mgr.Start(ctx)
}

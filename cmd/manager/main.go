package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	webhookserver "sigs.k8s.io/controller-runtime/pkg/webhook"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
	"github.com/openjobspec/ojs-k8s-operator/internal/controller"
	"github.com/openjobspec/ojs-k8s-operator/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

type runtimeOptions struct {
	leaderElection bool
	zapOptions     zap.Options
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ojsv1alpha1.AddToScheme(scheme))
}

func parseRuntimeOptions(args []string, output io.Writer) (runtimeOptions, error) {
	options := runtimeOptions{
		zapOptions: zap.Options{Development: true},
	}
	flags := flag.NewFlagSet("manager", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(
		&options.leaderElection,
		"leader-elect",
		false,
		"Enable leader election for controller manager. Enabling this ensures there is only one active controller manager.",
	)
	options.zapOptions.BindFlags(flags)

	if err := flags.Parse(args); err != nil {
		return runtimeOptions{}, err
	}
	if flags.NArg() != 0 {
		return runtimeOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return options, nil
}

func newManagerOptions(options runtimeOptions, webhooksEnabled bool, webhookCertDir string) ctrl.Options {
	managerOptions := ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: ":8081",
		LeaderElection:         options.leaderElection,
		LeaderElectionID:       "ojs-k8s-operator.openjobspec.dev",
	}
	if webhooksEnabled {
		managerOptions.WebhookServer = webhookserver.NewServer(webhookserver.Options{
			CertDir: webhookCertDir,
		})
	}
	return managerOptions
}

func main() {
	options, err := parseRuntimeOptions(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to parse manager flags: %v\n", err)
		os.Exit(2)
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&options.zapOptions)))

	webhooksEnabled := os.Getenv("ENABLE_WEBHOOKS") != "false"
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), newManagerOptions(
		options,
		webhooksEnabled,
		os.Getenv("WEBHOOK_CERT_DIR"),
	))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.OJSClusterReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("ojscluster-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "OJSCluster")
		os.Exit(1)
	}

	if err = (&controller.OJSWorkerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("ojsworker-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "OJSWorker")
		os.Exit(1)
	}

	// Register validating webhooks (disabled by setting ENABLE_WEBHOOKS=false)
	if webhooksEnabled {
		if err = ctrl.NewWebhookManagedBy(mgr).
			For(&ojsv1alpha1.OJSCluster{}).
			WithValidator(&webhook.OJSClusterValidator{}).
			Complete(); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "OJSCluster")
			os.Exit(1)
		}
		if err = ctrl.NewWebhookManagedBy(mgr).
			For(&ojsv1alpha1.OJSWorker{}).
			WithValidator(&webhook.OJSWorkerValidator{}).
			Complete(); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "OJSWorker")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

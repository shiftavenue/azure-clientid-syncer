package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/shiftavenue/aks-clientid-syncer/pkg/metrics"
	"github.com/shiftavenue/aks-clientid-syncer/pkg/util"
	"github.com/shiftavenue/aks-clientid-syncer/pkg/version"
	wh "github.com/shiftavenue/aks-clientid-syncer/pkg/webhook"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	kzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	klog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsServer "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var webhooks = []rotator.WebhookInfo{
	{
		Name: "aks-clientid-syncer-webhook-mutating-webhook-configuration",
		Type: rotator.Mutating,
	},
}

const (
	secretName     = "aks-clientid-syncer-webhook-server-cert" // #nosec
	serviceName    = "aks-clientid-syncer-webhook-webhook-service"
	caName         = "aks-clientid-syncer-ca"
	caOrganization = "aks-clientid-syncer"
)

var (
	webhookCertDir      string
	healthAddr          string
	metricsAddr         string
	disableCertRotation bool
	metricsBackend      string
	logLevel            int

	// DNSName is <service name>.<namespace>.svc
	dnsName = fmt.Sprintf("%s.%s.svc", serviceName, util.GetNamespace())
	scheme  = runtime.NewScheme()

	entryLog = kzap.New().WithName("entrypoint")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

func main() {
	if err := mainErr(); err != nil {
		log.Fatalln(err)
	}
}

func mainErr() error {
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/certs", "Webhook certificates dir to use. Defaults to /certs")
	flag.BoolVar(&disableCertRotation, "disable-cert-rotation", false, "disable automatic generation and rotation of webhook TLS certificates/keys")
	flag.StringVar(&healthAddr, "health-addr", ":9440", "The address the health endpoint binds to")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8095", "The address the metrics endpoint binds to")
	flag.StringVar(&metricsBackend, "metrics-backend", "prometheus", "Backend used for metrics")
	flag.IntVar(&logLevel, "log-level", 0,
		"A zap log level should be multiplied by -1 to get the logr verbosity. For example, to get logr verbosity of 3, pass zapcore.Level(-3) to this Opts. See https://pkg.go.dev/github.com/go-logr/zapr for how zap level relates to logr verbosity.")
	flag.Parse()

	ctx := signals.SetupSignalHandler()
	log := kzap.New(kzap.Level(zapcore.Level(logLevel)))

	klog.SetLogger(log)

	config := ctrl.GetConfigOrDie()
	config.UserAgent = version.GetUserAgent("webhook")

	// initialize metrics exporter before creating measurements
	entryLog.Info("initializing metrics backend", "backend", metricsBackend)
	if err := metrics.InitMetricsExporter(metricsBackend); err != nil {
		return fmt.Errorf("entrypoint: failed to initialize metrics exporter: %w", err)
	}

	// log the user agent as it makes it easier to debug issues
	entryLog.Info("setting up manager", "userAgent", config.UserAgent)
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		LeaderElection:         false,
		HealthProbeBindAddress: healthAddr,
		Logger:                 kzap.New().WithName("manager"),
		Metrics: metricsServer.Options{
			BindAddress: metricsAddr,
			CertDir:     webhookCertDir,
		},
		WebhookServer: &webhook.DefaultServer{
			Options: webhook.Options{
				CertDir: webhookCertDir,
			},
		},
		MapperProvider: func(c *rest.Config, httpClient *http.Client) (meta.RESTMapper, error) {
			return apiutil.NewDynamicRESTMapper(c, httpClient)
		},
	})
	if err != nil {
		return fmt.Errorf("entrypoint: unable to set up controller manager: %w", err)
	}

	// Make sure certs are generated and valid if cert rotation is enabled.
	setupFinished := make(chan struct{})
	if !disableCertRotation {
		entryLog.Info("setting up cert rotation")
		if err := rotator.AddRotator(mgr, &rotator.CertRotator{
			SecretKey: types.NamespacedName{
				Namespace: util.GetNamespace(),
				Name:      secretName,
			},
			CertDir:        webhookCertDir,
			CAName:         caName,
			CAOrganization: caOrganization,
			DNSName:        dnsName,
			IsReady:        setupFinished,
			Webhooks:       webhooks,
		}); err != nil {
			return fmt.Errorf("entrypoint: unable to set up cert rotation: %w", err)
		}
	} else {
		close(setupFinished)
	}

	setupProbeEndpoints(mgr, setupFinished)
	go setupWebhook(mgr, setupFinished, log)

	entryLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("entrypoint: unable to run manager: %w", err)
	}

	return nil
}

func setupWebhook(mgr manager.Manager, setupFinished chan struct{}, log logr.Logger) {
	// Block until the setup (certificate generation) finishes.
	<-setupFinished

	hookServer := mgr.GetWebhookServer()

	// setup webhooks
	entryLog.Info("registering webhook to the webhook server")
	serviceAccountMutator, err := wh.NewServiceAccountMutator(mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme(), log)
	if err != nil {
		panic(fmt.Errorf("unable to set up serviceaccount mutator: %w", err))
	}
	hookServer.Register("/mutate-v1-serviceaccount", &webhook.Admission{Handler: serviceAccountMutator})
}

func setupProbeEndpoints(mgr ctrl.Manager, setupFinished chan struct{}) {
	// Block readiness on the mutating webhook being registered.
	// We can't use mgr.GetWebhookServer().StartedChecker() yet,
	// because that starts the webhook. But we also can't call AddReadyzCheck
	// after Manager.Start. So we need a custom ready check that delegates to
	// the real ready check after the cert has been injected and validator started.
	checker := func(req *http.Request) error {
		select {
		case <-setupFinished:
			return mgr.GetWebhookServer().StartedChecker()(req)
		default:
			return fmt.Errorf("certs are not ready yet")
		}
	}

	if err := mgr.AddHealthzCheck("healthz", checker); err != nil {
		panic(fmt.Errorf("unable to add healthz check: %w", err))
	}
	if err := mgr.AddReadyzCheck("readyz", checker); err != nil {
		panic(fmt.Errorf("unable to add readyz check: %w", err))
	}
	entryLog.Info("added healthz and readyz check")
}

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	clog "github.com/ninnemana/godns/log"
	"github.com/ninnemana/hue-exporter/collector"
	prom "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/exporters/metric/prometheus"
	"go.uber.org/zap"
)

var (
	promPort = flag.String("metric-port", "8080", "indicates the port for Prometheus metrics to be served")

	defaultPort = "8080"
)

func main() {
	flag.Parse()

	logConfig := zap.NewDevelopmentConfig()
	logConfig.Encoding = "json"

	logger, err := logConfig.Build()
	if err != nil {
		log.Fatalf("failed to create structured logger: %v", err)
	}

	defer func() {
		_ = logger.Sync()
	}()

	if promPort == nil {
		promPort = &defaultPort
	}

	flush, err := initTracer("hue")
	if err != nil {
		logger.Fatal("failed to start tracer", zap.Error(err))
	}
	defer flush()

	reg := prom.NewRegistry()
	exporter, err := prometheus.InstallNewPipeline(prometheus.Config{
		Registry:   reg,
		Registerer: prom.WrapRegistererWithPrefix("hue_", reg),
	})
	if err != nil {
		logger.Fatal("failed to start metric meter", zap.Error(err))
	}

	coll, err := collector.NewGatherer(
		collector.WithLogger(&clog.Contextual{
			Logger: logger,
		}),
		collector.WithExporter(exporter),
		collector.WithHueConfig(collector.HueConfig{
			IP:       os.Getenv("HUE_ADDRESS"),
			Username: os.Getenv("HUE_USERNAME"),
		}),
	)
	if err != nil {
		logger.Fatal("failed to create collector", zap.Error(err))
	}

	go func() {
		for {
			if err := http.ListenAndServe(":"+*promPort, coll); err != nil {
				logger.Error("fell out of serving HTTP traffic", zap.Error(err))
			}

			time.Sleep(time.Second * 10)
		}
	}()

	logger.Info("Starting metric collector")

	if err := coll.Run(context.Background()); err != nil {
		logger.Fatal("fell out", zap.Error(err))
	}
}

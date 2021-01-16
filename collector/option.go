package collector

import (
	"time"

	"github.com/amimof/huego"
	log "github.com/ninnemana/godns/log"
	"go.opentelemetry.io/otel/exporters/metric/prometheus"
)

type Option func(*Gatherer)

func WithLogger(l *log.Contextual) Option {
	return func(c *Gatherer) {
		c.log = l
	}
}

func WithTicker(d time.Duration) Option {
	return func(c *Gatherer) {
		c.ticker = time.NewTicker(d)
	}
}

func WithExporter(ex *prometheus.Exporter) Option {
	return func(c *Gatherer) {
		c.meter = ex.MeterProvider().Meter("hue")
		c.exporter = ex
	}
}

func WithHueConfig(cfg HueConfig) Option {
	return func(c *Gatherer) {
		c.hue = huego.New(cfg.IP, cfg.Username)
	}
}

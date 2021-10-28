package collector

import (
	"time"

	"github.com/amimof/huego"
	"github.com/ninnemana/tracelog"
	"go.opentelemetry.io/otel/metric"
)

type Option func(*Gatherer)

func WithLogger(l *tracelog.TraceLogger) Option {
	return func(c *Gatherer) {
		c.log = l
	}
}

func WithTicker(d time.Duration) Option {
	return func(c *Gatherer) {
		c.ticker = time.NewTicker(d)
	}
}

func WithExporter(ex metric.MeterProvider) Option {
	return func(c *Gatherer) {
		c.meter = ex.Meter("hue")
		// c.exporter = ex
	}
}

func WithHueConfig(cfg HueConfig) Option {
	return func(c *Gatherer) {
		c.hue = huego.New(cfg.IP, cfg.Username)
	}
}

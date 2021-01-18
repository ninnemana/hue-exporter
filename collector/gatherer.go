package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/amimof/huego"
	log "github.com/ninnemana/godns/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/metric/prometheus"
	"go.opentelemetry.io/otel/label"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/unit"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

var (
	tracer = otel.GetTracerProvider().Tracer("collector")
)

type HueConfig struct {
	IP       string
	Username string
}

type Gatherer struct {
	log      *log.Contextual
	exporter *prometheus.Exporter
	meter    metric.Meter
	ticker   *time.Ticker
	hue      *huego.Bridge
	jobs     []CollectJob
}

func NewGatherer(opts ...Option) (Collector, error) {
	g := &Gatherer{
		ticker: time.NewTicker(time.Second * 5),
	}
	for _, opt := range opts {
		opt(g)
	}

	if err := g.valid(); err != nil {
		return nil, err
	}

	g.jobs = []CollectJob{
		&lights{
			log:   g.log,
			meter: g.meter,
			hue:   g.hue,
		},
		&groups{
			log:   g.log,
			meter: g.meter,
			hue:   g.hue,
		},
		&sensors{
			log:   g.log,
			meter: g.meter,
			hue:   g.hue,
		},
	}

	return g, nil
}

var (
	// ErrInvalidLogger is thrown when the logger provided does not satisfy
	// requirements.
	ErrInvalidLogger = errors.New("the provided logger is not valid")
	// ErrInvalidExporter is thrown when the Prometheus exporter provided
	// does not satisfy requirements.
	ErrInvalidExporter = errors.New("the provided *prometheus.Exporter is not valid")
)

func (g Gatherer) valid() error {
	if g.log == nil {
		return ErrInvalidLogger
	}

	if g.exporter == nil {
		return ErrInvalidExporter
	}

	return nil
}

func (g *Gatherer) Run(ctx context.Context) error {
	for {
		ctx, span := tracer.Start(ctx, "collector/gatherer.Run")

		grp, _ := errgroup.WithContext(ctx)

		for _, job := range g.jobs {
			grp.Go(job.Collect(ctx))
		}

		if err := grp.Wait(); err != nil {
			g.log.Error(ctx, "job failed to collect metrics", zap.Error(err))
		}

		select {
		case <-g.ticker.C:
			span.End()
		case <-ctx.Done():
			err := ctx.Err()
			if err != nil {
				g.log.Error(ctx, "context was cancelled", zap.Error(err))
			}
			span.End()

			return ctx.Err()
		}
	}
}

func (g *Gatherer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.exporter.ServeHTTP(w, r)
}

type CollectJob interface {
	Collect(context.Context) func() error
}

type lights struct {
	log   *log.Contextual
	hue   *huego.Bridge
	meter metric.Meter
}

func (l *lights) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "lights.Collect")
	return func() error {
		defer span.End()

		lights, err := l.hue.GetLightsContext(ctx)
		if err != nil {
			l.log.Error(ctx, "failed to fetch lights", zap.Error(err))

			return err
		}

		l.log.Info(ctx, "collecting lights", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64ValueObserver(
			"light",
			lightObserver(lights),
			metric.WithDescription("Number of lights in the current state. Includes brightness, identifer, and on state."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			l.log.Error(ctx, "failed to record light count", zap.Error(err))

			return fmt.Errorf("failed to collect light count: %w", err)
		}

		l.log.Info(ctx, "collecting light brightness", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64ValueObserver(
			"light_brightness",
			lightBrightnessObserver(lights),
			metric.WithDescription("Brightness of lights."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			l.log.Error(ctx, "failed to record light brightness", zap.Error(err))

			return fmt.Errorf("failed to collect light brightness: %w", err)
		}

		l.log.Info(ctx, "collected light metrics")

		newLights, err := l.hue.GetNewLightsContext(ctx)
		if err != nil {
			l.log.Error(ctx, "failed to fetch new lights", zap.Error(err))

			return err
		}

		l.log.Info(ctx, "collecting new lights", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64ValueObserver(
			"new_light",
			newLightObserver(newLights),
			metric.WithDescription("Number of new lights."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			l.log.Error(ctx, "failed to record new light count", zap.Error(err))

			return fmt.Errorf("failed to collect new light count: %w", err)
		}

		return nil
	}
}

func lightObserver(lights []huego.Light) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		for _, l := range lights {
			res.Observe(
				1,
				label.Bool("on", l.State.On),
				label.Int("id", l.ID),
				label.Uint("bri", uint(l.State.Bri)),
			)
		}
	}
}

func lightBrightnessObserver(lights []huego.Light) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		for _, l := range lights {
			res.Observe(
				int64(l.State.Bri),
				label.Bool("on", l.State.On),
				label.Int("id", l.ID),
			)
		}
	}
}

func newLightObserver(v *huego.NewLight) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		for _, l := range v.Lights {
			res.Observe(
				1,
				label.String("name", l),
				label.String("lastScan", v.LastScan),
			)
		}
	}
}

type groups struct {
	log   *log.Contextual
	hue   *huego.Bridge
	meter metric.Meter
}

func (g *groups) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "groups.Collect")
	return func() error {
		defer span.End()

		groups, err := g.hue.GetGroupsContext(ctx)
		if err != nil {
			g.log.Error(ctx, "failed to fetch groups", zap.Error(err))

			return err
		}

		g.log.Info(ctx, "collecting groups", zap.Int("count", len(groups)))
		if _, err := g.meter.NewInt64ValueObserver(
			"group",
			groupObserver(groups),
			metric.WithDescription("Number of groups in the current state. Includes brightness, identifer, and on state."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			g.log.Error(ctx, "failed to record group count", zap.Error(err))

			return fmt.Errorf("failed to collect group count: %w", err)
		}

		g.log.Info(ctx, "collected group metrics")

		return nil
	}
}

func groupObserver(groups []huego.Group) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		for _, g := range groups {
			res.Observe(
				1,
				label.Bool("on", g.State.On),
				label.Int("id", g.ID),
				label.Uint("bri", uint(g.State.Bri)),
			)
		}
	}
}

type sensors struct {
	log   *log.Contextual
	hue   *huego.Bridge
	meter metric.Meter
}

func (s *sensors) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "sensors.Collect")
	return func() error {
		defer span.End()

		sensors, err := s.hue.GetSensorsContext(ctx)
		if err != nil {
			s.log.Error(ctx, "failed to fetch sensors", zap.Error(err))

			return err
		}

		s.log.Info(ctx, "collecting sensors", zap.Int("count", len(sensors)))
		if _, err := s.meter.NewInt64ValueObserver(
			"sensors",
			sensorObserver(sensors),
		); err != nil {
			s.log.Error(ctx, "failed to record group count", zap.Error(err))

			return fmt.Errorf("failed to collect group count: %w", err)
		}

		s.log.Info(ctx, "collected group metrics")

		return nil
	}
}

func sensorObserver(sensors []huego.Sensor) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		for _, s := range sensors {
			res.Observe(
				1,
				label.String("type", s.Type),
				label.Int("id", s.ID),
			)
		}
	}
}

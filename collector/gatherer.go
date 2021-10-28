package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/amimof/huego"
	"github.com/ninnemana/tracelog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/unit"

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
	log    *tracelog.TraceLogger
	meter  metric.Meter
	ticker *time.Ticker
	hue    *huego.Bridge
	jobs   []CollectJob
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
)

func (g Gatherer) valid() error {
	if g.log == nil {
		return ErrInvalidLogger
	}

	return nil
}

func (g *Gatherer) Run(ctx context.Context) error {
	for {
		ctx, span := tracer.Start(ctx, "collector/gatherer.Run")
		log := g.log.SetContext(ctx)

		grp, _ := errgroup.WithContext(ctx)

		for _, job := range g.jobs {
			grp.Go(job.Collect(ctx))
		}

		if err := grp.Wait(); err != nil {
			log.Error("job failed to collect metrics", zap.Error(err))
		}

		select {
		case <-g.ticker.C:
			span.End()
		case <-ctx.Done():
			err := ctx.Err()
			if err != nil {
				log.Error("context was cancelled", zap.Error(err))
			}
			span.End()

			return ctx.Err()
		}
	}
}

func (g *Gatherer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// g.exporter.ServeHTTP(w, r)
}

type CollectJob interface {
	Collect(context.Context) func() error
}

type lights struct {
	log   *tracelog.TraceLogger
	hue   *huego.Bridge
	meter metric.Meter
}

func (l *lights) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "lights.Collect")
	log := l.log.SetContext(ctx)
	return func() error {
		defer span.End()

		hueGroups, err := l.hue.GetGroupsContext(ctx)
		if err != nil {
			log.Error("failed to fetch groups", zap.Error(err))

			return err
		}

		var groups lightGroups
		for _, group := range hueGroups {
			groups = append(groups, lightGroup{group})
		}

		lights, err := l.hue.GetLightsContext(ctx)
		if err != nil {
			log.Error("failed to fetch lights", zap.Error(err))

			return err
		}

		log.Info("collecting lights", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64GaugeObserver(
			"light",
			lightObserver(lights, groups),
			metric.WithDescription("Number of lights in the current state. Includes brightness, identifer, and on state."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			log.Error("failed to record light count", zap.Error(err))

			return fmt.Errorf("failed to collect light count: %w", err)
		}

		log.Info("collecting light brightness", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64GaugeObserver(
			"light_brightness",
			lightBrightnessObserver(lights, groups),
			metric.WithDescription("Brightness of lights."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			log.Error("failed to record light brightness", zap.Error(err))

			return fmt.Errorf("failed to collect light brightness: %w", err)
		}

		log.Info("collected light metrics")

		newLights, err := l.hue.GetNewLightsContext(ctx)
		if err != nil {
			log.Error("failed to fetch new lights", zap.Error(err))

			return err
		}

		log.Info("collecting new lights", zap.Int("count", len(lights)))
		if _, err := l.meter.NewInt64GaugeObserver(
			"new_light",
			newLightObserver(newLights),
			metric.WithDescription("Number of new lights."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			log.Error("failed to record new light count", zap.Error(err))

			return fmt.Errorf("failed to collect new light count: %w", err)
		}

		return nil
	}
}

type lightGroups []lightGroup

func (lgs lightGroups) lightExists(id int) *lightGroup {
	for _, g := range lgs {
		if g.lightExists(id) {
			return &g
		}
	}

	return nil
}

type lightGroup struct {
	huego.Group
}

func (lg *lightGroup) lightExists(id int) bool {
	for _, light := range lg.Group.Lights {
		if light == strconv.Itoa(id) {
			return true
		}
	}

	return false
}

func lightObserver(lights []huego.Light, groups lightGroups) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		if len(lights) == 0 {
			res.Observe(0)

			return
		}

		for _, l := range lights {
			var assignedGroup string

			// check if this light has been assigned a group
			if group := groups.lightExists(l.ID); group != nil {
				assignedGroup = group.Group.Name
			}

			res.Observe(
				1,
				attribute.Bool("on", l.State.On),
				attribute.Int("id", l.ID),
				attribute.String("group", assignedGroup),
			)
		}
	}
}

func lightBrightnessObserver(lights []huego.Light, groups lightGroups) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		if len(lights) == 0 {
			res.Observe(0)

			return
		}

		for _, l := range lights {
			var assignedGroup string

			// check if this light has been assigned a group
			if group := groups.lightExists(l.ID); group != nil {
				assignedGroup = group.Group.Name
			}
			res.Observe(
				int64(l.State.Bri),
				attribute.Bool("on", l.State.On),
				attribute.Int("id", l.ID),
				attribute.String("group", assignedGroup),
			)
		}
	}
}

func newLightObserver(v *huego.NewLight) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		if len(v.Lights) == 0 {
			res.Observe(
				0,
				attribute.String("lastScan", v.LastScan),
			)

			return
		}

		for _, l := range v.Lights {
			res.Observe(
				1,
				attribute.String("name", l),
				attribute.String("lastScan", v.LastScan),
			)
		}
	}
}

type groups struct {
	log   *tracelog.TraceLogger
	hue   *huego.Bridge
	meter metric.Meter
}

func (g *groups) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "groups.Collect")
	log := g.log.SetContext(ctx)

	return func() error {
		defer span.End()

		groups, err := g.hue.GetGroupsContext(ctx)
		if err != nil {
			log.Error("failed to fetch groups", zap.Error(err))

			return err
		}

		log.Info("collecting groups", zap.Int("count", len(groups)))
		if _, err := g.meter.NewInt64GaugeObserver(
			"group",
			groupObserver(groups),
			metric.WithDescription("Number of groups in the current state. Includes brightness, identifer, and on state."),
			metric.WithUnit(unit.Dimensionless),
		); err != nil {
			log.Error("failed to record group count", zap.Error(err))

			return fmt.Errorf("failed to collect group count: %w", err)
		}

		log.Info("collected group metrics")

		return nil
	}
}

func groupObserver(groups []huego.Group) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		if len(groups) == 0 {
			res.Observe(0)

			return
		}

		for _, g := range groups {
			res.Observe(
				1,
				attribute.Bool("on", g.State.On),
				attribute.Int("id", g.ID),
				attribute.Int("bri", int(g.State.Bri)),
				attribute.String("name", g.Name),
			)
		}
	}
}

type sensors struct {
	log   *tracelog.TraceLogger
	hue   *huego.Bridge
	meter metric.Meter
}

func (s *sensors) Collect(ctx context.Context) func() error {
	ctx, span := tracer.Start(ctx, "sensors.Collect")
	log := s.log.SetContext(ctx)

	return func() error {
		defer span.End()

		sensors, err := s.hue.GetSensorsContext(ctx)
		if err != nil {
			log.Error("failed to fetch sensors", zap.Error(err))

			return err
		}

		log.Info("collecting sensors", zap.Int("count", len(sensors)))
		if _, err := s.meter.NewInt64GaugeObserver(
			"sensors",
			sensorObserver(sensors),
		); err != nil {
			log.Error("failed to record group count", zap.Error(err))

			return fmt.Errorf("failed to collect group count: %w", err)
		}

		log.Info("collected group metrics")

		return nil
	}
}

func sensorObserver(sensors []huego.Sensor) metric.Int64ObserverFunc {
	return func(ctx context.Context, res metric.Int64ObserverResult) {
		if len(sensors) == 0 {
			res.Observe(0)

			return
		}

		for _, s := range sensors {
			res.Observe(
				1,
				attribute.String("type", s.Type),
				attribute.Int("id", s.ID),
			)
		}
	}
}

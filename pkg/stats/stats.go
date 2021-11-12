package stats

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/mitchellh/hashstructure/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	metric_sdk "go.opentelemetry.io/otel/sdk/export/metric"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	selector "go.opentelemetry.io/otel/sdk/metric/selector/simple"
)

const (
	ebpfMeter = "ebpf.solo.io"
)

type PrometheusOpts struct {
	Port        uint32
	MetricsPath string
}

func (p *PrometheusOpts) initDefaults() {
	if p.Port == 0 {
		p.Port = 9091
	}
	if p.MetricsPath == "" {
		p.MetricsPath = "/metrics"
	}
}

func NewPrometheusMetricsProvider(ctx context.Context, opts *PrometheusOpts) (MetricsProvider, error) {
	opts.initDefaults()
	config := prometheus.Config{}
	// TODO: Figure out these options
	c := controller.New(
		processor.NewFactory(
			selector.NewWithExactDistribution(),
			metric_sdk.CumulativeExportKindSelector(),
			processor.WithMemory(true),
		),
	)
	exporter, err := prometheus.New(config, c)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize prometheus exporter %v", err)
	}
	global.SetMeterProvider(exporter.MeterProvider())

	meter := exporter.MeterProvider().Meter(ebpfMeter)
	serveMux := http.NewServeMux()
	serveMux.HandleFunc(opts.MetricsPath, exporter.ServeHTTP)
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", opts.Port),
		Handler: serveMux,
	}
	go func() {
		_ = server.ListenAndServe()
	}()

	go func() {
		<-ctx.Done()
		server.Close()
	}()

	return &metricsProvider{meter: meter}, nil
}

type MetricsProvider interface {
	NewSetCounter(name string) SetInstrument
	NewIncrementCounter(name string) IncrementInstrument
	NewGauge(name string) SetInstrument
}

type IncrementInstrument interface {
	Increment(ctx context.Context, labels map[string]string)
}

type SetInstrument interface {
	Set(ctx context.Context, val int64, labels map[string]string)
}

type metricsProvider struct {
	meter metric.Meter
}

func (m *metricsProvider) NewSetCounter(name string) SetInstrument {
	counter := metric.Must(m.meter).NewInt64Counter(name)
	return &setCounter{
		counter:    counter,
		counterMap: map[uint64]int64{},
	}
}

func (m *metricsProvider) NewIncrementCounter(name string) IncrementInstrument {
	return &incrementCounter{
		counter: metric.Must(m.meter).NewInt64Counter(name),
	}
}

func (m *metricsProvider) NewGauge(name string) SetInstrument {
	val := new(int64)
	labels := new([]attribute.KeyValue)
	observerLock := &sync.RWMutex{}
	_ = metric.Must(m.meter).NewInt64GaugeObserver(name, func(c context.Context, ior metric.Int64ObserverResult) {
		(*observerLock).RLock()
		value := *val
		labels := *labels
		(*observerLock).RUnlock()
		ior.Observe(value, labels...)
	})
	return &gauge{
		val:    val,
		labels: labels,
		lock:   observerLock,
	}
}

type setCounter struct {
	counter    metric.Int64Counter
	counterMap map[uint64]int64
}

func (c *setCounter) Set(
	ctx context.Context,
	intVal int64,
	decodedKey map[string]string,
) {

	labels := transformLabels(decodedKey)
	keyHash, err := hashstructure.Hash(decodedKey, hashstructure.FormatV2, nil)
	if err != nil {
		log.Fatal("This should never happen")
	}

	oldVal := c.counterMap[keyHash]
	diff := intVal - oldVal
	if oldVal == intVal {
		return
	}
	c.counterMap[keyHash] = intVal
	c.counter.Add(ctx, int64(diff), labels...)
}

type incrementCounter struct {
	counter metric.Int64Counter
}

func (i *incrementCounter) Increment(
	ctx context.Context,
	decodedKey map[string]string,
) {
	labels := transformLabels(decodedKey)
	i.counter.Add(ctx, 1, labels...)
}

type gauge struct {
	val    *int64
	labels *[]attribute.KeyValue
	lock   *sync.RWMutex
}

func (g *gauge) Set(
	ctx context.Context,
	intVal int64,
	decodedKey map[string]string,
) {

	labels := transformLabels(decodedKey)

	(*g.lock).Lock()
	defer (*g.lock).Unlock()
	*g.labels = labels
	*g.val = intVal
}

func transformLabels(
	decodedKey map[string]string,
) []attribute.KeyValue {

	labels := []attribute.KeyValue{}
	for k, v := range decodedKey {
		valAsStr := fmt.Sprint(v)
		thisKv := attribute.String(k, valAsStr)
		labels = append(labels, thisKv)
	}

	return labels
}
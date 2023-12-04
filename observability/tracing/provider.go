package tracing

import (
	"context"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var (
	DefaultPropagator Propagator     = propagation.TraceContext{}
	DefaultProvider   TracerProvider = NewNoopProvider()
)

const (
	RequestId = attribute.Key("request_id")
)

func NewProviderFromConfiguration(ctx context.Context, config Config) (Provider, error) {
	if !config.Enable {
		return NewNoopProvider(), nil
	}

	exporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpoint(config.Address),
	)
	if err != nil {
		return nil, errors.WithMessage(err, "new otlp http exporter")
	}

	attributes := []attribute.KeyValue{
		semconv.DeploymentEnvironment(config.Environment),
		semconv.ServiceVersion(config.ModuleVersion),
		semconv.ServiceName(config.ModuleName),
		semconv.ServiceInstanceID(config.InstanceId),
	}
	for key, value := range config.Attributes {
		attributes = append(attributes, attribute.String(key, value))
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			attributes...,
		),
	)
	if err != nil {
		return nil, errors.WithMessage(err, "new resource")
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), //TODO consider configuration, but pass all for now
	)
	return provider, nil
}

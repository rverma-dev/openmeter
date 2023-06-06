package router

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/render"
	"golang.org/x/exp/slog"

	"github.com/openmeterio/openmeter/api"
	"github.com/openmeterio/openmeter/internal/models"
	"github.com/openmeterio/openmeter/internal/streaming"
)

func init() {
	// See https://github.com/getkin/kin-openapi/issues/640
	openapi3filter.RegisterBodyDecoder("application/cloudevents+json", jsonBodyDecoder)
}

func jsonBodyDecoder(body io.Reader, header http.Header, schema *openapi3.SchemaRef, encFn openapi3filter.EncodingFn) (interface{}, error) {
	var value interface{}
	if err := json.NewDecoder(body).Decode(&value); err != nil {
		return nil, &openapi3filter.ParseError{Kind: openapi3filter.KindInvalidFormat, Cause: err}
	}
	return value, nil
}

type Config struct {
	StreamingConnector streaming.Connector
	Meters             []*models.Meter
}

type Router struct {
	config *Config
}

// Make sure we conform to ServerInterface
var _ api.ServerInterface = (*Router)(nil)

func NewRouter(config *Config) (*Router, error) {
	return &Router{
		config: config,
	}, nil
}

func (a *Router) IngestEvents(w http.ResponseWriter, r *http.Request) {
	var event event.Event
	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		slog.Error("unable to parse event", "error", err)
		_ = render.Render(w, r, api.ErrInternalServerError(err))
		return
	}

	if event.Time().IsZero() {
		event.SetTime(time.Now().UTC())
	}

	err = a.config.StreamingConnector.Publish(event)
	if err != nil {
		slog.Error("unable to produce event to Kafka", "error", err)
		_ = render.Render(w, r, api.ErrInternalServerError(err))
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (a *Router) GetMeters(w http.ResponseWriter, r *http.Request) {
	if err := render.RenderList(w, r, NewMeterListResponse(a.config.Meters)); err != nil {
		_ = render.Render(w, r, api.ErrUnprocessableEntity(err))
		return
	}
}

func NewMeterListResponse(meters []*models.Meter) []render.Renderer {
	list := make([]render.Renderer, 0, len(meters))
	for _, m := range meters {
		list = append(list, m)
	}
	return list
}

func (a *Router) GetMetersById(w http.ResponseWriter, r *http.Request, meterID string) {
	println(meterID)
	for _, meter := range a.config.Meters {
		if meter.ID == meterID {
			if err := render.Render(w, r, meter); err != nil {
				_ = render.Render(w, r, api.ErrUnprocessableEntity(err))
			}
			return
		}
	}

	_ = render.Render(w, r, api.ErrNotFound)
}

type GetValuesByMeterIdResponse struct {
	Values []*models.MeterValue `json:"values"`
}

func NewGetValuesByMeterIdResponse(values []*models.MeterValue) *GetValuesByMeterIdResponse {
	return &GetValuesByMeterIdResponse{
		Values: values,
	}
}

func (rd *GetValuesByMeterIdResponse) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}

func (a *Router) GetValuesByMeterId(w http.ResponseWriter, r *http.Request, meterId string, params api.GetValuesByMeterIdParams) {
	for _, meter := range a.config.Meters {
		if meter.ID == meterId {
			values, err := a.config.StreamingConnector.GetValues(meter, &streaming.GetValuesParams{
				From:    params.From,
				To:      params.To,
				Subject: params.Subject,
			})
			if err != nil {
				_ = render.Render(w, r, api.ErrInternalServerError(err))
				return
			}

			if err := render.Render(w, r, NewGetValuesByMeterIdResponse(values)); err != nil {
				_ = render.Render(w, r, api.ErrUnprocessableEntity(err))
				return
			}

			return
		}
	}

	_ = render.Render(w, r, api.ErrNotFound)
}
package collector

import (
	"context"
	"net/http"
)

type Collector interface {
	http.Handler
	Run(ctx context.Context) error
}

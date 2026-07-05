package playground

import (
	"context"
	"errors"
	"net/url"

	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// resourceFilterIDResult carries a parsed resource filter ID and match state.
type resourceFilterIDResult struct {
	id        int64
	present   bool
	matchable bool
}

// queryPlayspecFilterID resolves playspec_id list filters by ID or name.
func (h Handler) queryPlayspecFilterID(ctx context.Context, q url.Values) (resourceFilterIDResult, error) {
	return queryResourceFilterID(q, "playspec_id", func(identifier string) (int64, error) {
		ps, err := h.repo.GetPlayspec(ctx, identifier)
		if err != nil {
			return 0, err
		}
		if ps.ID == nil {
			return 0, store.ErrNotFound
		}
		return *ps.ID, nil
	})
}

// queryMarqueeFilterID resolves marquee_id filters within configured scope.
func (h Handler) queryMarqueeFilterID(ctx context.Context, q url.Values) (resourceFilterIDResult, error) {
	filter, err := resourceFilterValue(q, "marquee_id")
	if err != nil || !filter.present {
		return resourceFilterIDResult{present: filter.present}, err
	}
	resolved, err := h.resolveMarqueeFilterID(ctx, filter.value)
	resolved.present = true
	return resolved, err
}

// resolveMarqueeFilterID resolves one configured-scope Marquee filter value.
func (h Handler) resolveMarqueeFilterID(ctx context.Context, value string) (resourceFilterIDResult, error) {
	configured, configuredOK, err := h.configuredMarquee(ctx)
	if err != nil {
		return resourceFilterIDResult{}, err
	}
	numeric, err := positiveResourceFilterID(value, "marquee_id")
	if err != nil {
		return resourceFilterIDResult{}, err
	}
	if numeric.numeric {
		if !configuredOK {
			return resourceFilterIDResult{}, nil
		}
		return resourceFilterIDResult{id: configured.ID, matchable: true}, nil
	}
	return h.resolveNamedMarqueeFilterID(ctx, value, configured, configuredOK)
}

// configuredMarquee returns the startup-configured runtime Marquee.
func (h Handler) configuredMarquee(ctx context.Context) (domain.Marquee, bool, error) {
	return h.repo.GetRuntimeMarquee(ctx)
}

// resolveNamedMarqueeFilterID resolves a Marquee name filter inside scope.
func (h Handler) resolveNamedMarqueeFilterID(ctx context.Context, value string, configured domain.Marquee, configuredOK bool) (resourceFilterIDResult, error) {
	if !configuredOK {
		return resourceFilterIDResult{}, nil
	}
	mq, err := h.repo.GetMarquee(ctx, value)
	if errors.Is(err, store.ErrNotFound) {
		return resourceFilterIDResult{}, nil
	}
	if err != nil {
		return resourceFilterIDResult{}, err
	}
	if mq.ID != configured.ID {
		return resourceFilterIDResult{id: mq.ID}, nil
	}
	return resourceFilterIDResult{id: mq.ID, matchable: true}, nil
}

// queryResourceFilterID resolves positive numeric IDs or resource names.
func queryResourceFilterID(q url.Values, key string, lookup func(string) (int64, error)) (resourceFilterIDResult, error) {
	filter, err := resourceFilterValue(q, key)
	if err != nil || !filter.present {
		return resourceFilterIDResult{present: filter.present}, err
	}
	if numeric, err := positiveResourceFilterID(filter.value, key); err != nil {
		return resourceFilterIDResult{present: true}, err
	} else if numeric.numeric {
		return resourceFilterIDResult{id: numeric.id, present: true, matchable: true}, nil
	}
	id, err := lookup(filter.value)
	if errors.Is(err, store.ErrNotFound) {
		return resourceFilterIDResult{present: true}, nil
	}
	if err != nil {
		return resourceFilterIDResult{present: true}, err
	}
	return resourceFilterIDResult{id: id, present: true, matchable: true}, nil
}

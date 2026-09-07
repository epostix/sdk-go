package epostix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const maximumPages = 10000

type Page[T any] struct {
	Data                    []T
	HasMore                 bool
	NextCursor              string
	ContinuationUnavailable bool
	Meta                    ResponseMeta
}

type Collection[T any] struct {
	Items      []T
	Truncated  bool
	LastCursor string
}

type rawPage struct {
	Data       json.RawMessage `json:"data"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
	meta       ResponseMeta
}

func (p *rawPage) setMeta(meta ResponseMeta) {
	p.meta = meta
}

type Iterator[T any] struct {
	ctx     context.Context
	client  *Client
	input   *paginateInput
	opts    []RequestOption
	filters url.Values

	page       []T
	index      int
	cursor     string
	pageNumber int
	done       bool
	err        error
	current    T
	lastMeta   ResponseMeta
}

func newIterator[T any](
	ctx context.Context,
	client *Client,
	input *paginateInput,
	opts ...RequestOption,
) *Iterator[T] {
	filters := url.Values{}
	for key, values := range input.query {
		if key == "ending_before" {
			continue
		}

		for _, value := range values {
			filters.Add(key, value)
		}
	}

	iterator := &Iterator[T]{
		ctx: ctx, client: client, input: input, opts: opts, filters: filters,
	}

	if input.query.Get("ending_before") != "" {
		iterator.err = &ConfigError{
			Message: fmt.Sprintf(
				"%s: ending_before cannot be used with an iterator, which walks forward only",
				input.op.ID),
		}
		iterator.done = true
	}

	return iterator
}

func (it *Iterator[T]) Next() bool {
	if it.err != nil {
		return false
	}

	if it.index < len(it.page) {
		it.current = it.page[it.index]
		it.index++

		return true
	}

	if it.done {
		return false
	}

	if !it.fetch() {
		return false
	}

	return it.Next()
}

func (it *Iterator[T]) fetch() bool {
	it.pageNumber++
	if it.pageNumber > maximumPages {
		it.err = &PaginationError{
			Reason: ReasonPageLimitExceeded, Operation: it.input.op.ID,
			Message: fmt.Sprintf("stopped after %d pages", maximumPages),
		}

		return false
	}

	query := url.Values{}
	for key, values := range it.filters {
		for _, value := range values {
			query.Add(key, value)
		}
	}

	if it.cursor != "" {
		query.Set(it.input.cursorParam, it.cursor)
	}

	page, err := execute[rawPage](it.ctx, it.client, &requestInput{
		op:    it.input.op,
		path:  it.input.path,
		query: query,
	}, it.opts...)
	if err != nil {
		it.err = err

		return false
	}

	it.lastMeta = page.meta

	var items []T
	if len(page.Data) > 0 {
		if err := json.Unmarshal(page.Data, &items); err != nil {
			it.err = &TransportError{Message: "the page data could not be decoded", Cause: err}

			return false
		}
	}

	it.page = items
	it.index = 0

	if !page.HasMore {
		it.done = true

		return true
	}

	if page.NextCursor == "" {
		it.err = &PaginationError{
			Reason: ReasonMissingCursor, Operation: it.input.op.ID,
			Message: "the server reported more results but returned no cursor",
		}

		return false
	}

	if it.cursor != "" && page.NextCursor == it.cursor {
		it.err = &PaginationError{
			Reason: ReasonRepeatedCursor, Operation: it.input.op.ID,
			Message: "the server returned the same cursor it was given",
		}

		return false
	}

	it.cursor = page.NextCursor

	return true
}

func (it *Iterator[T]) Current() T {
	return it.current
}

func (it *Iterator[T]) Err() error {
	return it.err
}

func (it *Iterator[T]) Meta() ResponseMeta {
	return it.lastMeta
}

func (it *Iterator[T]) Collect(maximumItems int) (*Collection[T], error) {
	if maximumItems <= 0 {
		return nil, &ConfigError{Message: "Collect requires a positive maximumItems"}
	}

	collection := &Collection[T]{Items: make([]T, 0, maximumItems)}

	for it.Next() {
		if len(collection.Items) >= maximumItems {
			collection.Truncated = true
			collection.LastCursor = it.cursor

			return collection, nil
		}

		collection.Items = append(collection.Items, it.Current())
	}

	if err := it.Err(); err != nil {
		return nil, err
	}

	collection.LastCursor = it.cursor

	return collection, nil
}

func (it *Iterator[T]) All() func(func(T, error) bool) {
	return func(yield func(T, error) bool) {
		for it.Next() {
			if !yield(it.Current(), nil) {
				return
			}
		}

		if err := it.Err(); err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

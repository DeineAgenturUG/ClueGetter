// ClueGetter - Does things with mail
//
// Copyright 2016 Dolf Schimmel, Freeaqingme.
//
// This Source Code Form is subject to the terms of the two-clause BSD license.
// For its contents, please refer to the LICENSE file.
//
package elasticsearch

import (
	"encoding/json"

	"cluegetter/address"

	"gopkg.in/olivere/elastic.v3"
	"time"
)

func (m *module) getSessionsByAddress(instances []string, address *address.Address) ([]*session, error) {
	query := elastic.NewBoolQuery().Must(
		elastic.NewTermsQuery("InstanceId", stringSliceToIface(instances)...),
		elastic.NewNestedQuery("Messages",
			elastic.NewBoolQuery().Should(
				addressQuery("Messages.From", address),
				elastic.NewNestedQuery("Messages.Rcpt",
					addressQuery("Messages.Rcpt", address),
				),
			),
		),
	)

	sr, err := m.esClient.Search().
		Index("cluegetter-sessions").
		Query(query).
		Sort("DateConnect", false).
		From(0).Size(250).
		//Pretty(true).
		Do()
	if err != nil {
		return nil, err
	}

	sessions := make([]*session, 0)
	if sr == nil || sr.TotalHits() == 0 {
		return sessions, nil
	}

	for _, hit := range sr.Hits.Hits {
		session := &session{}
		if err := json.Unmarshal(*hit.Source, session); err != nil {
			return nil, err
		}
		for _, msg := range session.Messages {
			msg.SetSession(session.MilterSession)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

type Finder struct {
	module *module

	from          *address.Address
	to            *address.Address
	saslUser      string
	clientAddress string
	dateStart     *time.Time
	dateEnd       *time.Time
	instances     []string
}

type FinderResponse struct {
	Total    int64
	Sessions []session

	DateHistogram24Hrs map[int64]int64
}

func (m *module) NewFinder() *Finder {
	return &Finder{
		module: m,

		from: &address.Address{},
		to:   &address.Address{},
	}
}

func (f *Finder) From() *address.Address {
	return f.from
}

func (f *Finder) To() *address.Address {
	return f.to
}

func (f *Finder) SaslUser() string {
	return f.saslUser
}

func (f *Finder) ClientAddress() string {
	return f.clientAddress
}

func (f *Finder) DateStart() *time.Time {
	return f.dateStart
}

func (f *Finder) DateEnd() *time.Time {
	return f.dateEnd
}

func (f *Finder) Instances() []string {
	return f.instances
}

func (f *Finder) SetFrom(from *address.Address) *Finder {
	f.from = from
	return f
}

func (f *Finder) SetTo(to *address.Address) *Finder {
	f.to = to
	return f
}

func (f *Finder) SetSaslUser(user string) *Finder {
	f.saslUser = user
	return f
}

func (f *Finder) SetClientAddress(ip string) *Finder {
	f.clientAddress = ip
	return f
}

func (f *Finder) SetDateStart(start *time.Time) *Finder {
	f.dateStart = start
	return f
}

func (f *Finder) SetDateEnd(end *time.Time) *Finder {
	f.dateEnd = end
	return f
}

func (f *Finder) SetInstances(instances []string) *Finder {
	f.instances = instances
	return f
}

func (f *Finder) Find() (*FinderResponse, error) {
	resp := &FinderResponse{}

	search := f.module.esClient.Search().
		Index("cluegetter-sessions").
		Sort("DateConnect", false).
		From(0).
		Size(250)
	//Pretty(true).

	f.query(search)
	f.aggs(search)

	sr, err := search.Do()
	if err != nil {
		return resp, err
	}

	resp.Total = sr.Hits.TotalHits
	resp.Sessions, err = f.decodeSessions(sr)
	if err != nil {
		return resp, err
	}

	aggParent, _ := sr.Aggregations.Nested("DateHistogram24Hrs")
	agg, _ := aggParent.DateHistogram("sessions")
	resp.DateHistogram24Hrs = make(map[int64]int64)
	for _, bucket := range agg.Buckets {
		resp.DateHistogram24Hrs[bucket.Key] = bucket.DocCount
	}

	return resp, nil
}

func (f *Finder) aggs(service *elastic.SearchService) *elastic.SearchService {
	dateAgg := elastic.NewDateHistogramAggregation().
		Field("DateConnect").
		Interval("15m").
		Format("yyyy-MM-dd HH:mm").
		TimeZone("CET") // Do more intelligently?
	filter := elastic.NewRangeQuery("DateConnect").
		Gt("now-24h")
	agg := elastic.NewFilterAggregation().Filter(filter).
		SubAggregation("sessions", dateAgg)
	service = service.Aggregation("DateHistogram24Hrs", agg)

	/*
		dateAgg = elastic.NewDateHistogramAggregation().
			Field("DateConnect").
			Interval("2h").
			Format("yyyy-MM-dd HH:mm").
			TimeZone("CET") // Do more intelligently?
		filter = elastic.NewRangeQuery("DateConnect").
			Gt("now-30d")
		agg = elastic.NewFilterAggregation().Filter(filter).
			SubAggregation("sessions", dateAgg)
		service = service.Aggregation("DateHistogram30Days", agg)

		dateAgg = elastic.NewDateHistogramAggregation().
			Field("DateConnect").
			Interval("1d").
			Format("yyyy-MM-dd HH:mm").
			TimeZone("CET") // Do more intelligently?
		filter = elastic.NewRangeQuery("DateConnect").
			Gt("now-365d")
		agg = elastic.NewFilterAggregation().Filter(filter).
			SubAggregation("sessions", dateAgg)
		service = service.Aggregation("DateHistogram1Yrs", agg)
	*/

	return service
}

func (f *Finder) query(service *elastic.SearchService) *elastic.SearchService {
	q := elastic.NewBoolQuery()
	q.Must(elastic.NewTermsQuery("InstanceId", stringSliceToIface(f.instances)...))

	qMsg := elastic.NewBoolQuery()
	if f.from.String() != "" {
		qMsg.Must(addressQuery("Messages.From", f.from))
	}
	if f.to.String() != "" {
		qMsg.Must(elastic.NewNestedQuery("Messages.Rcpt",
			addressQuery("Messages.Rcpt", f.to),
		))
	}
	q.Must(elastic.NewNestedQuery("Messages", qMsg))

	// saslUser
	// clientAddress

	return service.Query(q)
}

func (f *Finder) decodeSessions(sr *elastic.SearchResult) ([]session, error) {
	sessions := make([]session, 0)
	if sr == nil || sr.TotalHits() == 0 {
		return sessions, nil
	}

	for _, hit := range sr.Hits.Hits {
		session := &session{}
		if err := json.Unmarshal(*hit.Source, session); err != nil {
			return nil, err
		}
		for _, msg := range session.Messages {
			msg.SetSession(session.MilterSession)
		}
		sessions = append(sessions, *session)
	}

	return sessions, nil
}

func stringSliceToIface(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func addressQuery(prefix string, address *address.Address) elastic.Query {
	if address.Local() == "" {
		return elastic.NewMatchQuery(prefix+".Domain", address.Domain())
	}

	return elastic.NewBoolQuery().Must(
		elastic.NewMatchQuery(prefix+".Local", address.Local()),
		elastic.NewMatchQuery(prefix+".Domain", address.Domain()),
	)
}

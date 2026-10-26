// Package seed generates the deterministic fictional dataset (docs/contracts/seed.md):
// customers, their cart/purchase/page-view history, the deliberate edge groups and the
// manifest of expected counts that tests read (REQ-SEED-01, REQ-SEED-02).
package seed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const MinUsers = 200

type Options struct {
	Seed    int64
	Users   int
	RefTime time.Time // UTC, second precision
}

var (
	ErrTooFewUsers     = errors.New("seed: too few users")
	ErrInvalidRefTime  = errors.New("seed: invalid ref time")
	ErrDatasetMismatch = errors.New("seed: database holds a different dataset; use --reset")
)

// idNamespace is the UUIDv5 namespace of every seeded id (seed.md §2).
var idNamespace = uuid.MustParse("3f6c1b52-8a3e-5d2f-9b7c-2e4d6a8f0c11")

const day = 24 * time.Hour

// Customer is one seeded customer row.
type customer struct {
	index         int
	id            string
	firstName     string
	lastName      string
	email         *string
	phone         *string
	city          string
	language      string
	loyaltyTier   string
	smsOptIn      bool
	smsOptInAt    *time.Time
	emailOptIn    bool
	emailOptInAt  *time.Time
	createdAt     time.Time
	lastSeenAt    *time.Time
	group         string // fixed edge group name, "" for the random population
	events        []event
	matchesBrief  bool
	inSMSAudience bool
	inEmailAud    bool
}

type event struct {
	id          string
	customerID  string
	typ         string
	occurredAt  time.Time
	amountCents int64 // purchase only
	category    *string
}

// Dataset is a generated seed dataset.
type Dataset struct {
	opts      Options
	customers []customer // index order
	jsonl     []byte
	manifest  Manifest
}

// Generate is pure and deterministic: equal Options => byte-identical WriteJSONL output and manifest.
// Errors: ErrTooFewUsers (< MinUsers), ErrInvalidRefTime (zero time).
func Generate(opts Options) (*Dataset, error) {
	if opts.Users < MinUsers {
		return nil, fmt.Errorf("%w: %d < %d", ErrTooFewUsers, opts.Users, MinUsers)
	}
	if opts.RefTime.IsZero() {
		return nil, ErrInvalidRefTime
	}
	opts.RefTime = opts.RefTime.UTC().Truncate(time.Second)

	g := &generator{
		opts: opts,
		rng:  rand.New(rand.NewPCG(uint64(opts.Seed), uint64(opts.Seed)^0x9e3779b97f4a7c15)), //nolint:gosec // deterministic fictional data, not security
	}
	g.fixedGroups()
	for len(g.customers) < opts.Users {
		g.randomCustomer()
	}
	for i := range g.customers {
		evaluate(&g.customers[i], opts.RefTime)
	}

	d := &Dataset{opts: opts, customers: g.customers}
	var buf bytes.Buffer
	if err := d.writeJSONL(&buf); err != nil {
		return nil, err
	}
	d.jsonl = buf.Bytes()
	sum := sha256.Sum256(d.jsonl)
	m, err := buildManifest(opts, d.customers, hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, err
	}
	d.manifest = m
	return d, nil
}

// WriteJSONL writes customers then events, each sorted by id, one JSON object per line.
func (d *Dataset) WriteJSONL(w io.Writer) error {
	_, err := w.Write(d.jsonl)
	return err
}

// Manifest returns the expected counts of the dataset.
func (d *Dataset) Manifest() Manifest { return d.manifest }

// sha256Hex is the dataset identity stored in seed_runs.
func (d *Dataset) sha256Hex() string { return d.manifest.DatasetSHA256 }

type generator struct {
	opts      Options
	rng       *rand.Rand
	customers []customer
}

func (g *generator) ref() time.Time { return g.opts.RefTime }

func (g *generator) customerID(index int) string {
	return uuid.NewSHA1(idNamespace, []byte("scc-seed/"+strconv.FormatInt(g.opts.Seed, 10)+"/customer/"+strconv.Itoa(index))).String()
}

func (g *generator) eventID(customerIndex, n int) string {
	return uuid.NewSHA1(idNamespace, []byte("scc-seed/"+strconv.FormatInt(g.opts.Seed, 10)+"/event/"+strconv.Itoa(customerIndex)+"/"+strconv.Itoa(n))).String()
}

// phoneFor gives every index its own number in the fictional block, skipping the slot reserved
// for the shared_contact pair. Unique for up to 9999 customers.
func phoneFor(index int) string {
	slot := index % sharedPhoneSlot
	return fmt.Sprintf("+90500000%04d", slot)
}

func (g *generator) emailFor(index int, first, last string) string {
	domain := "example.com"
	if g.rng.IntN(10) == 0 {
		domain = "example.test"
	}
	local := ""
	if f := emailLocal(first); f != "" {
		local = f + "."
	}
	if l := emailLocal(last); l != "" {
		local += l + "."
	}
	return local + strconv.Itoa(index) + "@" + domain
}

func ptr[T any](v T) *T { return &v }

// base draws the attributes shared by fixed-group and random customers.
func (g *generator) base(group string) customer {
	index := len(g.customers)
	first := firstNames[g.rng.IntN(len(firstNames))]
	last := lastNames[g.rng.IntN(len(lastNames))]
	createdAt := g.ref().Add(-time.Duration(30+g.rng.IntN(971)) * day).Add(-time.Duration(g.rng.IntN(86400)) * time.Second)
	c := customer{
		index:       index,
		id:          g.customerID(index),
		firstName:   first,
		lastName:    last,
		city:        istanbul,
		language:    g.language(),
		loyaltyTier: g.tier(),
		createdAt:   createdAt,
		group:       group,
	}
	c.email = ptr(g.emailFor(index, first, last))
	c.phone = ptr(phoneFor(index))
	if g.rng.IntN(10) > 0 {
		c.lastSeenAt = ptr(g.between(createdAt, g.ref()))
	}
	return c
}

func (g *generator) language() string {
	if g.rng.IntN(10) == 0 {
		return "en"
	}
	return "tr"
}

func (g *generator) tier() string {
	switch n := g.rng.IntN(100); {
	case n < 50:
		return "bronze"
	case n < 80:
		return "silver"
	case n < 95:
		return "gold"
	default:
		return "platinum"
	}
}

// between returns a second-precision time uniformly in [from, to].
func (g *generator) between(from, to time.Time) time.Time {
	span := int64(to.Sub(from) / time.Second)
	if span <= 0 {
		return from
	}
	return from.Add(time.Duration(g.rng.Int64N(span+1)) * time.Second)
}

func (g *generator) setConsent(c *customer, sms, email bool) {
	c.smsOptIn, c.emailOptIn = sms, email
	c.smsOptInAt, c.emailOptInAt = nil, nil
	if sms {
		c.smsOptInAt = ptr(g.between(c.createdAt, g.ref()))
	}
	if email {
		c.emailOptInAt = ptr(g.between(c.createdAt, g.ref()))
	}
}

func (g *generator) addEvent(c *customer, typ string, at time.Time) {
	e := event{
		id:         g.eventID(c.index, len(c.events)),
		customerID: c.id,
		typ:        typ,
		occurredAt: at,
		category:   ptr(productCategories[g.rng.IntN(len(productCategories))]),
	}
	if typ == "purchase" {
		e.amountCents = 5000 + g.rng.Int64N(495001) // 50.00 .. 5000.00 TRY
	}
	c.events = append(c.events, e)
}

// fixedGroups emits the edge groups of seed.md §3.3, from index 0, in table order.
func (g *generator) fixedGroups() {
	ref := g.ref()
	add := func(group string, n int, tweak func(c *customer)) {
		for range n {
			c := g.base(group)
			g.setConsent(&c, true, true)
			tweak(&c)
			g.customers = append(g.customers, c)
		}
	}
	cartAt := func(d time.Duration) func(c *customer) {
		return func(c *customer) { g.addEvent(c, "cart_add", ref.Add(-d)) }
	}
	add("cart_add_29d", 5, cartAt(29*day))
	add("cart_add_30d", 5, cartAt(30*day))
	add("cart_add_31d", 5, cartAt(31*day))
	add("cart_add_then_purchase", 10, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-10*day))
		g.addEvent(c, "purchase", ref.Add(-5*day))
	})
	add("purchase_before_cart_add", 6, func(c *customer) {
		g.addEvent(c, "purchase", ref.Add(-20*day))
		g.addEvent(c, "cart_add", ref.Add(-10*day))
	})
	add("istanbul_no_consent", 10, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		g.setConsent(c, false, false)
	})
	add("istanbul_sms_only", 8, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		g.setConsent(c, true, false)
	})
	add("istanbul_email_only", 8, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		g.setConsent(c, false, true)
	})
	add("sms_consent_missing_phone", 4, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		g.setConsent(c, true, false)
		c.phone = nil
	})
	add("email_consent_missing_email", 4, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		g.setConsent(c, false, true)
		c.email = nil
	})
	add("empty_first_name", 6, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		c.firstName = ""
	})
	add("long_first_name", 3, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		c.firstName = longFirstName
	})
	add("shared_contact", 2, func(c *customer) {
		g.addEvent(c, "cart_add", ref.Add(-7*day))
		c.phone = ptr(sharedPhone)
		c.email = ptr(sharedEmail)
	})
	add("non_istanbul_cart_add", 10, func(c *customer) {
		c.city = "Ankara"
		g.addEvent(c, "cart_add", ref.Add(-3*day))
	})
}

// randomCustomer draws one member of the random population (seed.md §3.2).
func (g *generator) randomCustomer() {
	c := g.base("")
	if g.rng.IntN(100) >= 30 {
		c.city = otherCities[g.rng.IntN(len(otherCities))]
	}
	switch n := g.rng.IntN(100); {
	case n < 50:
		g.setConsent(&c, true, true)
	case n < 65:
		g.setConsent(&c, true, false)
	case n < 80:
		g.setConsent(&c, false, true)
	default:
		g.setConsent(&c, false, false)
	}
	if g.rng.IntN(100) < 5 {
		c.phone = nil
	}
	if g.rng.IntN(100) < 3 {
		c.email = nil
	}
	from, to := g.ref().Add(-120*day), g.ref().Add(-time.Hour)
	times := make([]time.Time, g.rng.IntN(13))
	for i := range times {
		times[i] = g.between(from, to)
	}
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	for _, at := range times {
		var typ string
		switch n := g.rng.IntN(100); {
		case n < 45:
			typ = "cart_add"
		case n < 85:
			typ = "page_view"
		default:
			typ = "purchase"
		}
		g.addEvent(&c, typ, at)
	}
	g.customers = append(g.customers, c)
}

// facts are the DSL-visible statistics of one customer at ref (customer_stats_at semantics).
type facts struct {
	lastCartAddAt *time.Time
	orders30d     int
}

func factsAt(c *customer, ref time.Time) facts {
	var f facts
	windowStart := ref.Add(-30 * day)
	for _, e := range c.events {
		if e.occurredAt.After(ref) {
			continue
		}
		switch e.typ {
		case "cart_add":
			if f.lastCartAddAt == nil || e.occurredAt.After(*f.lastCartAddAt) {
				f.lastCartAddAt = ptr(e.occurredAt)
			}
		case "purchase":
			if !e.occurredAt.Before(windowStart) {
				f.orders30d++
			}
		}
	}
	return f
}

// evaluate applies the canonical example-brief rule (segment-dsl.md §7.1) and the channel
// consent filter (segment-dsl.md §6). It mirrors the SQL path (customer_stats_at + the compiled
// rule), so the manifest is an independent cross-check of the database counts.
func evaluate(c *customer, ref time.Time) {
	f := factsAt(c, ref)
	c.matchesBrief = c.city == istanbul &&
		f.lastCartAddAt != nil &&
		!f.lastCartAddAt.Before(ref.Add(-30*day)) && !f.lastCartAddAt.After(ref) &&
		f.orders30d == 0
	c.inSMSAudience = c.matchesBrief && c.smsOptIn && c.phone != nil
	c.inEmailAud = c.matchesBrief && c.emailOptIn && c.email != nil
}

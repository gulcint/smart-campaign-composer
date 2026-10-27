# Go arayüzleri ve test dikişleri

Kapsam: tüm Go unit/entegrasyon testlerinin dokunabileceği **tek** Go yüzeyi. Burada olmayan exported ad testlerde kullanılmaz.

Modül: `github.com/gulcint/smart-campaign-composer` · Go ≥ 1.25 · Anthropic SDK `github.com/anthropics/anthropic-sdk-go/v2` (yalnız `internal/llm` içinde) · rastgelelik `math/rand/v2` · log `log/slog` · DB sürücüsü `github.com/jackc/pgx/v5` (`pgxpool`).

## 0. Genel kurallar

- **Testler kara kutudur:** her paketin testi `package <pkg>_test`'tir (ör. `package content_test`); unexported adlar test edilmez.
- **Hatalar:** her paket sentinel hatalarını `var ErrXxx = errors.New("<pkg>: ...")` olarak tanımlar; testler **yalnızca** `errors.Is` (ve alan hataları için `errors.As(err, &*validation.Error)`) kullanır, mesaj metnine bakmaz. Sarmalanmış hatalar `%w` ile sarılır.
- **Zaman:** hiçbir paket `time.Now()`/`time.Sleep`/`time.After` doğrudan kullanmaz; `clock.Clock` enjekte edilir.
- **Rastgelelik:** global `math/rand` yok; `*rand.Rand` (`math/rand/v2`) enjekte edilir. Enjeksiyon noktaları: `delivery.NewRetryPolicy`, `mockprovider.New` (Config.Seed), `seed.Generate` (Options.Seed).
- **Ağ:** testlerde yalnızca `httptest`. Gerçek Claude istemcisi test binary'sinde kurulamaz: `llm.New` `llm.ErrRealClientForbidden` döner (panik **değil**; PLAN §4.3'teki "panik" ifadesinin yerine geçer). Test tarafı koşum düzeni (test yazarının yazdığı ortak yardımcı `test/testenv`, fonksiyon `testenv.Main(m *testing.M)`; LLM fabrikası kuran her test paketinin `TestMain`'i bunu çağırır) `llm.New`'i ortam yapılandırmasıyla bir kez çağırır ve `errors.Is(err, llm.ErrRealClientForbidden)` ise mesajı stderr'e yazıp **`os.Exit(1)`** ile çıkar; böylece "gerçek istemci seçilmeye çalışılırsa test süreci hata koduyla sonlanır" (REQ-DLV-02) gözlemlenebilir olur.
- **Context:** bloklayan her fonksiyon ilk parametre olarak `ctx context.Context` alır; iptalde `ctx.Err()` (sarmalanmış olabilir; `errors.Is(err, context.Canceled)` doğru döner).

Requirements'taki adlarla eşleme: `LLMClient` = `llm.Client`; `Clock` = `clock.Clock`; doğrulayıcı = `content.Validate` / `segment.Validate`; retry politikası = `delivery.RetryPolicy`; kuyruk = `delivery.Store` + `deliveries` tablosu.

## 1. Paket düzeni

| Paket | Sorumluluk | Test türü |
|---|---|---|
| `internal/clock` | `Clock`, gerçek ve sahte saat | unit |
| `internal/secret` | Maskelenen `Secret` tipi | unit |
| `internal/config` | Ortam değişkenlerinden yapılandırma | unit |
| `internal/validation` | `FieldError`, `Error`, ortak kodlar | unit |
| `internal/segment` | DSL: Parse, Validate, Compile, Match | unit |
| `internal/content` | Şablon: Parse, Validate, Render, en kötü durum, email HTML | unit |
| `internal/llm` | `Client` arayüzü, sağlayıcı seçimi, fabrika, sahte LLM | unit |
| `internal/composer` | LLM + doğrulama + tek düzeltme döngüsü, içerik olayları | unit (sahte LLM) |
| `internal/campaign` | Durum makinesi + kampanya servisi | unit (`Transition`), entegrasyon (servis) |
| `internal/delivery` | Retry politikası, hız sınırlayıcı, dispatcher, kuyruk deposu arayüzü | unit (sahte saat/depo/provider), entegrasyon |
| `internal/provider` | Provider HTTP istemcisi | unit (`httptest`) |
| `internal/mockprovider` | Mock provider HTTP sunucusu | unit (`httptest`) |
| `internal/seed` | Deterministik veri + manifest + yükleme | unit (Generate), entegrasyon (Load) |
| `internal/postgres` | pgx tabanlı depo implementasyonları | entegrasyon |
| `internal/migrate` | Migration up/down | entegrasyon |
| `internal/sse` | SSE yazıcısı | unit |
| `internal/health` | `/healthz`, `/readyz` handler'ları | unit |
| `internal/metrics` | Prometheus kayıtları | unit |
| `internal/httpapi` | HTTP router (OpenAPI'nin implementasyonu) | unit (sahte `campaign.Service`), E2E |
| `cmd/api`, `cmd/worker`, `cmd/mockprovider`, `cmd/seed`, `cmd/migrate` | Binary'ler (`cmd/migrate` sahip kararı) | E2E / süreç |

Entegrasyon testleri `testing.Short()` iken kendini atlar ve DB'ye `TEST_DATABASE_URL` (`scc_app`) / `TEST_MIGRATE_DATABASE_URL` (`scc_migrator`) ile bağlanır ([README §7](README.md#7-test-ortamı-sözleşmesi)).

---

## `internal/clock`

```go
package clock

// Clock is the only source of time in the system.
type Clock interface {
	Now() time.Time                         // always UTC
	After(d time.Duration) <-chan time.Time // fires once when Now() >= start+d
}

func Real() Clock // wall clock

// Sleep blocks for d on c or until ctx is done (returns ctx.Err()).
func Sleep(ctx context.Context, c Clock, d time.Duration) error

// Fake is a manually driven clock for tests. Safe for concurrent use.
type Fake struct{ /* unexported */ }

func NewFake(start time.Time) *Fake
func (f *Fake) Now() time.Time
func (f *Fake) After(d time.Duration) <-chan time.Time // d <= 0 fires immediately
func (f *Fake) Advance(d time.Duration)                 // moves time forward, fires due waiters in deadline order
func (f *Fake) Set(t time.Time)                         // t must be >= Now(); panics otherwise
func (f *Fake) Waiters() int                            // number of pending After channels
// BlockUntilWaiters returns nil once Waiters() >= n, or ctx.Err().
func (f *Fake) BlockUntilWaiters(ctx context.Context, n int) error
```

Test referans zamanı: `2026-01-15T12:00:00Z`.

## `internal/secret`

```go
package secret

// Secret holds a sensitive string (API key, DB URL with password).
// Every formatting path is masked; only Reveal returns the raw value.
type Secret string

const Mask = "[REDACTED]"

func (s Secret) Reveal() string
func (s Secret) IsSet() bool                          // strings.TrimSpace(raw) != ""
func (s Secret) String() string                       // Mask if IsSet, "" otherwise  (%v, %s)
func (s Secret) GoString() string                     // same as String                 (%#v)
func (s Secret) LogValue() slog.Value                 // slog.StringValue(String())
func (s Secret) MarshalJSON() ([]byte, error)         // JSON string of String()
func (s Secret) MarshalText() ([]byte, error)         // String()
```

## `internal/config`

```go
package config

type Config struct {
	HTTPAddr        string        // HTTP_ADDR, default ":8080" (api)
	WorkerHTTPAddr  string        // WORKER_HTTP_ADDR, default ":8081" (worker health/metrics)
	DatabaseURL     secret.Secret // DATABASE_URL, required for api/worker
	LogLevel        slog.Level    // LOG_LEVEL: debug|info|warn|error, default info

	AnthropicAPIKey secret.Secret // ANTHROPIC_API_KEY, optional
	LLMMode         llm.Mode      // LLM_PROVIDER: auto|real|mock, default auto
	AnthropicModel  string        // ANTHROPIC_MODEL, default "claude-opus-5-5"
	LLMTimeout      time.Duration // LLM_TIMEOUT, default 60s
	LLMMaxTokens    int           // LLM_MAX_TOKENS, default 4096
	MockLLMScenario llm.Scenario  // MOCK_LLM_SCENARIO, default "ok"
	MockLLMChunkDelay time.Duration // MOCK_LLM_CHUNK_DELAY, default 0s

	SegmentReferenceTime *time.Time // SEGMENT_REFERENCE_TIME (RFC 3339), optional; nil => Clock.Now()

	ProviderBaseURL       string        // PROVIDER_BASE_URL, default "http://mockprovider:8090"
	ProviderTimeout       time.Duration // PROVIDER_TIMEOUT, default 10s
	DispatchRatePerSecond int           // DISPATCH_RATE_PER_SECOND, default 100 (per channel)
	DispatchConcurrency   int           // DISPATCH_CONCURRENCY, default 32
	DispatchBatchSize     int           // DISPATCH_BATCH_SIZE, default 50
	DeliveryLease         time.Duration // DELIVERY_LEASE, default 60s
	Retry                 delivery.RetryConfig // RETRY_MAX_ATTEMPTS(5), RETRY_BASE_DELAY(1s), RETRY_MAX_DELAY(60s),
	                                           // RETRY_MAX_RATE_LIMITED(20), RETRY_RATE_LIMIT_WINDOW(10m)
	WorkerRandSeed        int64         // WORKER_RAND_SEED, default 0 => seeded from crypto/rand
}

// Load reads configuration through getenv (tests pass a map lookup; binaries pass os.Getenv).
// Empty string == unset. Durations use time.ParseDuration syntax.
func Load(getenv func(string) string) (Config, error)

var (
	ErrInvalidValue = errors.New("config: invalid value") // wrapped with the variable name, e.g. "LLM_PROVIDER"
	ErrMissing      = errors.New("config: required variable missing")
)

// Config renders with secrets masked; output never contains a Secret's raw value.
func (c Config) String() string
func (c Config) LogValue() slog.Value
```

- `LLM_PROVIDER` geçersiz (ör. `fake`) → `ErrInvalidValue`.
- `Load`, `LLM_PROVIDER=real` + key yok durumunu **reddetmez**; bu karar `llm.Select`'tedir (tek yer).
- `DatabaseURL` eksikliği `Load`'da değil, onu kullanan binary'de (`ErrMissing` ile) kontrol edilir; böylece `Load` testleri DB'siz çalışır.

## `internal/validation`

```go
package validation

type FieldError struct {
	Path    string `json:"path"`              // e.g. "sms.body", "rule.all[0].field", "brief", "" (whole document)
	Code    string `json:"code"`              // closed list, README §5.2
	Message string `json:"message"`           // English, human readable, never contains secrets
	Limit   *int   `json:"limit,omitempty"`   // set for length codes
	Actual  *int   `json:"actual,omitempty"`  // set for length codes (rune count)
	Err     error  `json:"-"`                 // sentinel this field error matches
}

// Error aggregates field errors. errors.Is(err, X) is true if any FieldError.Err matches X.
type Error struct{ Fields []FieldError }

func (e *Error) Error() string
func (e *Error) Unwrap() []error

var (
	ErrInvalidJSON     = errors.New("validation: invalid json")     // code "invalid_json"
	ErrSchemaViolation = errors.New("validation: schema violation") // code "schema_violation"
	ErrRequired        = errors.New("validation: required")         // code "required"
	ErrTooLong         = errors.New("validation: too long")         // code "too_long"
	ErrInvalidValue    = errors.New("validation: invalid value")    // code "invalid_value"
)

// Code constants (string values are the wire codes).
const (
	CodeRequired = "required"; CodeTooLong = "too_long"; CodeInvalidValue = "invalid_value"
	CodeInvalidJSON = "invalid_json"; CodeSchemaViolation = "schema_violation"; CodeTruncatedOutput = "truncated_output"
	CodeNoToolCall = "no_tool_call"
	CodeLLMUnavailable = "llm_unavailable"; CodeInternalError = "internal_error"
	CodeSMSTooLong = "sms_too_long"; CodeSubjectTooLong = "subject_too_long"; CodeEmailBodyTooLong = "email_body_too_long"
	CodeSMSTooLongWorstCase = "sms_too_long_worst_case"; CodeSubjectTooLongWorstCase = "subject_too_long_worst_case"
	CodeUnknownVariable = "unknown_variable"; CodeMalformedVariable = "malformed_variable"
	CodeUnknownField = "unknown_field"; CodeUnknownOperator = "unknown_operator"; CodeOperatorNotAllowed = "operator_not_allowed"
	CodeTypeMismatch = "type_mismatch"; CodeEmptyGroup = "empty_group"
	CodeMaxDepthExceeded = "max_depth_exceeded"; CodeMaxConditionsExceeded = "max_conditions_exceeded"
)
```

## `internal/segment`

DSL ayrıntısı: [`segment-dsl.md`](segment-dsl.md).

```go
package segment

const (
	MaxDepth      = 3
	MaxConditions = 10
)

type FieldType string

const (
	TypeString FieldType = "string"; TypeEnum FieldType = "enum"; TypeInteger FieldType = "integer"
	TypeNumber FieldType = "number"; TypeTimestamp FieldType = "timestamp"
)

type FieldSpec struct {
	Name      string
	Type      FieldType
	Operators []string // allowed operators, segment-dsl.md §3
	Enum      []string // only for TypeEnum
}

func Fields() []FieldSpec // the 9 fields of segment-dsl.md §2, in that order

// Node is exactly one of: All, Any, Not (groups) or Cond (leaf).
type Node struct {
	All  []Node
	Any  []Node
	Not  *Node
	Cond *Condition
}

type Condition struct {
	Field string
	Op    string
	Value json.RawMessage // nil when absent
}

type Rule struct{ Root Node }

// MarshalJSON emits the root group (e.g. {"all":[...]}), i.e. the value of the "rule" property.
func (r Rule) MarshalJSON() ([]byte, error)

// Parse decodes a document of the form {"rule": <group>} (the emit_segment_rule tool input and
// the POST /api/segments/count body). Structural checks only, in one linear pass; it does not walk
// past the limits: a group nested deeper than MaxDepth+1 => max_depth_exceeded (path of the first
// group over MaxDepth, as Validate), more than MaxConditions+1 conditions => max_conditions_exceeded
// (path "rule"). At most 20 field errors. Errors: *validation.Error matching
// validation.ErrInvalidJSON or validation.ErrSchemaViolation (or those two limit sentinels).
func Parse(raw []byte) (Rule, error)

// Validate performs semantic checks and returns nil or a *validation.Error with every problem
// (segment-dsl.md §1.1, §3, §7.3). Field errors match these sentinels:
func Validate(r Rule) error

var (
	ErrUnknownField          = errors.New("segment: unknown field")          // "unknown_field"
	ErrUnknownOperator       = errors.New("segment: unknown operator")       // "unknown_operator"
	ErrOperatorNotAllowed    = errors.New("segment: operator not allowed")   // "operator_not_allowed"
	ErrTypeMismatch          = errors.New("segment: type mismatch")          // "type_mismatch"
	ErrEmptyGroup            = errors.New("segment: empty group")            // "empty_group"
	ErrMaxDepthExceeded      = errors.New("segment: max depth exceeded")     // "max_depth_exceeded"
	ErrMaxConditionsExceeded = errors.New("segment: max conditions exceeded")// "max_conditions_exceeded"
	// "invalid_value" field errors match validation.ErrInvalidValue.
)

type Compiled struct {
	Where string // SQL boolean expression over aliases c (customers) and s (customer_stats_at($1))
	Args  []any  // Args[0] == ref (time.Time, UTC); rule values follow as $2, $3, ...
}

// Compile validates r (returning Validate's error if invalid) and translates it to a
// parameterized predicate. No rule value ever appears in Where.
func Compile(r Rule, ref time.Time) (Compiled, error)

// Facts are one customer's DSL-visible attributes at a reference time (customer_stats_at semantics).
type Facts struct {
	City, Language, LoyaltyTier string
	LastCartAddAt, LastPurchaseAt *time.Time
	Orders30d, CartAdds30d, TotalOrders int
	TotalSpent float64
}

// Match evaluates r in memory with the same semantics as Compile + SQL (used by cmd/seed to
// compute manifest counts). r must be valid; otherwise Validate's error is returned.
func Match(r Rule, f Facts, ref time.Time) (bool, error)

type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

// Counter counts the audience of a valid rule at ref (channel consent applied, segment-dsl.md §6).
// Implemented by postgres.NewAudience.
type Counter interface {
	Count(ctx context.Context, r Rule, ref time.Time) (Audience, error)
}
```

## `internal/content`

```go
package content

const (
	SMSMaxRunes       = 160
	SubjectMaxRunes   = 80
	EmailBodyMaxRunes = 5000
	FirstNameFallback = "Değerli müşterimiz"
)

var AllowedVariables = []string{"first_name", "last_name", "city"}

type Content struct {
	SMS   SMS   `json:"sms"`
	Email Email `json:"email"`
}
type SMS struct {
	Body string `json:"body"`
}
type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Parse decodes content.schema.json documents (LLM tool input and edit request body) against the
// FULL schema's STRUCTURE. Unknown keys, wrong JSON types AND missing keys => schema_violation at
// that path (a missing key is never "required"; the path of an unknown key is the key itself, e.g.
// "html"). Parse does NOT enforce the schema's `minLength: 1`: an empty string is structurally valid
// and is reported by Validate as `required` (README §5.2), never as schema_violation. Strings are
// NFC-normalized and otherwise stored as given (no trimming). On a Parse error Validate is not run.
// Errors: *validation.Error matching validation.ErrInvalidJSON / ErrSchemaViolation.
func Parse(raw []byte) (Content, error)

// Validate is THE validator for both LLM output and human edits (REQ-APR-02, REQ-CNT-01).
// l = longest values of the campaign's CURRENT segment (Campaign.Segment.LongestValues).
// Checks per field, in order sms.body, email.subject, email.body:
//   required (present but empty after TrimSpace) -> malformed_variable -> unknown_variable -> length.
// At most ONE FieldError per field (the first that applies); the returned order is the field order
// above. A field with a required/malformed/unknown error gets no length check (substitution is
// undefined), other fields are still checked independently.
// The length rule is applied AFTER substitution: every placeholder is replaced by a value of the
// corresponding Longest length (first_name already includes the fallback) and the resulting rune
// count is compared with 160 (sms.body) / 80 (email.subject) / 5000 (email.body).
// Length FieldErrors carry Limit and Actual (= worst-case rendered length).
// Returns nil or *validation.Error; field errors match:
func Validate(c Content, l Longest) error

// WorstCase are the post-substitution lengths reported on every content version whose template
// syntax is valid (API field ContentVersion.worst_case).
type WorstCase struct {
	SMSRunes     int `json:"sms_runes"`
	SubjectRunes int `json:"subject_runes"`
}

// MeasureWorstCase computes WorstCase with the same substitution as Validate.
// Errors: ErrMalformedVariable, ErrUnknownVariable.
func MeasureWorstCase(c Content, l Longest) (WorstCase, error)

var (
	ErrSMSTooLong        = errors.New("content: sms too long")         // "sms_too_long", "sms_too_long_worst_case"
	ErrSubjectTooLong    = errors.New("content: subject too long")     // "subject_too_long", "subject_too_long_worst_case"
	ErrEmailBodyTooLong  = errors.New("content: email body too long")  // "email_body_too_long"
	ErrUnknownVariable   = errors.New("content: unknown variable")     // "unknown_variable"
	ErrMalformedVariable = errors.New("content: malformed variable")   // "malformed_variable"
	ErrWorstCaseTooLong  = errors.New("content: worst case too long")  // additionally matched by *_worst_case codes
	// empty fields match validation.ErrRequired ("required")
)

// RuneCount returns the number of Unicode code points of the NFC form of s.
func RuneCount(s string) int

// Variables returns the placeholder names in tmpl in order of appearance (duplicates kept).
// Grammar: "{{" [spaces] name [spaces] "}}", name = [A-Za-z_][A-Za-z0-9_]*.
// "{{" without a closing "}}", "{{ }}", a name with other characters (e.g. "{{first-name}}")
// or a "}}" that closes nothing => ErrMalformedVariable. Names are not checked against
// AllowedVariables (case-sensitive check happens in Validate/Render).
func Variables(tmpl string) ([]string, error)

type Recipient struct {
	FirstName, LastName, City string
}

// EffectiveFirstName returns strings.TrimSpace(s), or FirstNameFallback if that is empty.
func EffectiveFirstName(s string) string

// Render substitutes {{first_name}} (EffectiveFirstName), {{last_name}} and {{city}} (raw,
// possibly empty). Errors: ErrMalformedVariable, ErrUnknownVariable.
func Render(tmpl string, r Recipient) (string, error)

type Rendered struct {
	SMSText      string
	EmailSubject string
	EmailText    string
}

func RenderContent(c Content, r Recipient) (Rendered, error)

// CheckRendered is the per-recipient send-time check (REQ-CNT-05):
// sms_too_long / subject_too_long on the rendered text. nil or *validation.Error.
func CheckRendered(r Rendered) error

// Longest holds the longest value lengths (runes) over the customers MATCHED by the segment rule at
// its reference time (before consent filtering). FirstName MUST already include the fallback:
// max over matched of RuneCount(EffectiveFirstName(first_name)). Empty matched set =>
// Longest{FirstName: 18 /* RuneCount(FirstNameFallback) */, LastName: 0, City: 0}.
type Longest struct {
	FirstName int `json:"first_name"`
	LastName  int `json:"last_name"`
	City      int `json:"city"`
}

// CheckWorstCase is the approval-time RE-check (REQ-CNT-05) with Longest recomputed at approval
// time. Same substitution as Validate, only sms.body <= 160 and email.subject <= 80; codes
// sms_too_long_worst_case / subject_too_long_worst_case, Actual = worst-case length; they match
// ErrSMSTooLong/ErrSubjectTooLong AND ErrWorstCaseTooLong; paths "sms.body" / "email.subject",
// Limit 160 / 80. email.body (5000) is NOT re-checked at approval. Because Validate already applies
// the rule, this fails over HTTP only if customer data changed after validation; it is unit-tested directly.
func CheckWorstCase(c Content, l Longest) error

// RenderEmailHTML builds the escaped HTML document used for the provider and the preview:
//   <!doctype html><html lang="tr"><head><meta charset="utf-8">
//   <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
//   <title>{subject}</title></head><body><p>{paragraph}</p>...</body></html>
// Paragraphs are separated by one or more blank lines; a single "\n" inside a paragraph becomes "<br>".
// Every text fragment is escaped with html.EscapeString; the output never contains a tag that
// was not produced by this skeleton (no "<script", no "<a", no attributes from content).
func RenderEmailHTML(subject, bodyText string) string
```

**Değişken söz dizimi tablosu** (`Variables` → `Validate`; boşluk = yalnız U+0020 ve U+0009):

| Şablon | Sonuç |
|---|---|
| `{{first_name}}`, `{{ first_name }}`, `{{city }}` (araya sekme de girebilir), `{{first_name}}{{first_name}}` | geçerli (`Variables` sırayla ad döner, yinelenenler korunur) |
| `{first_name}`, `%30`, `$`, tek `{` veya tek `}` (ör. `a } b`) | düz metin (değişken yok) |
| `{{first_name`, `{{`, `{{ }}`, `{{}}`, `}}`, `{{first-name}}`, `{{1st}}`, `{{first name}}`, `{{{first_name}}}` | `malformed_variable` |
| `{{First_Name}}`, `{{password}}`, `{{email}}`, `{{ phone }}` | `unknown_variable` (söz dizimi geçerli, ad izinli listede yok; büyük/küçük harf duyarlı) |
| Aynı alanda hem bozuk hem bilinmeyen değişken | tek hata: `malformed_variable` |

`first_name` doldurma: `EffectiveFirstName` (kırpılmış değer, boşsa yedek metin). `last_name`, `city`: ham değer, boşsa boş metin. Doldurma sonucu tekrar şablon olarak işlenmez (değer içindeki `{{x}}` düz metindir).

Uzunluk ölçüsü örnekleri (değişkensiz metinde `Longest` etkisizdir): `strings.Repeat("ş",160)` geçerli, 161 → `sms_too_long` (`Actual` 161); `"İ"` (U+0130) 1 karakter; ayrışık `"I\u0307"` (Go kaçış dizisi) NFC'de `"İ"` olur ve 1 sayılır. `"{{first_name}}" + strings.Repeat("a",146)` (şablon 160) `Validate(c, Longest{FirstName:50})` ile **reddedilir** (`sms_too_long`, `Actual` 196), `Longest{FirstName:14}` ile geçer (160). Örnek brief kitlesinde `Longest` = manifest `example_brief.longest_values` (`first_name` 50, `city` 8) ve `CNT_OK` SMS'i tam 160'tır.

## `internal/llm`

Senaryolar ve fixture'lar: [`llm-tools.md`](llm-tools.md).

```go
package llm

type Mode string // configuration value of LLM_PROVIDER

const (
	ModeAuto Mode = "auto"
	ModeReal Mode = "real"
	ModeMock Mode = "mock"
)

func ParseMode(s string) (Mode, error) // "" => ModeAuto; unknown => ErrInvalidMode

type Provider string // effective provider, exposed by GET /api/config

const (
	ProviderMock   Provider = "mock"
	ProviderClaude Provider = "claude"
)

// Select is the pure provider decision (REQ-AI-03):
//   auto + key set  => ProviderClaude; auto + no key => ProviderMock
//   real + key set  => ProviderClaude; real + no key => ErrMissingAPIKey
//   mock            => ProviderMock (key ignored)
// "key set" == strings.TrimSpace(apiKey) != "".
func Select(mode Mode, apiKey string) (Provider, error)

type FactoryConfig struct {
	Mode       Mode
	APIKey     secret.Secret
	Model      string
	Timeout    time.Duration
	MaxTokens  int
	Mock       MockOptions
	HTTPClient *http.Client // nil => default
	Logger     *slog.Logger // nil => slog.Default()
}

// New selects and constructs the client. It logs exactly one INFO record
//   msg="llm provider selected" mode=<mode> provider=<mock|claude> api_key_present=<bool>
// and never logs the key (also not on error).
// "Forbidden" := testing.Testing() is true OR env SCC_FORBID_REAL_LLM == "1" (set in compose.test.yml).
// Error precedence (first match wins):
//   1. mode == ModeMock                      => Mock, ProviderMock, nil
//   2. forbidden && mode == ModeReal         => ErrRealClientForbidden (even without a key)
//   3. forbidden && mode == ModeAuto && key  => ErrRealClientForbidden
//   4. Select(mode, key) error               => ErrMissingAPIKey (real without key, not forbidden)
//   5. otherwise                             => Select's provider
// cmd/api exits with code 1 on any error; in tests see §0 (testenv.Main => os.Exit(1)).
func New(cfg FactoryConfig) (Client, Provider, error)

// ToolName values.
const (
	ToolEmitSegmentRule     = "emit_segment_rule"
	ToolEmitCampaignContent = "emit_campaign_content"
)

// ToolSchema returns the strict-compatible input_schema SENT to the model for a tool: the embedded
// bytes of schemas/llm/segment-rule.tool.json or schemas/llm/content.tool.json (llm-tools.md §1.1).
// Unknown name => ErrUnknownTool. The backend never validates against this schema; it validates
// against the FULL schemas (schemas/*.schema.json) via segment.Parse / content.Parse.
func ToolSchema(name string) (json.RawMessage, error)

// Client is one LLM attempt; validation and repair live in internal/composer.
type Client interface {
	// GenerateSegment returns the raw emit_segment_rule tool input (JSON bytes, possibly invalid).
	// Errors: ErrUnavailable, ErrTruncated, ErrNoToolCall, ctx errors.
	GenerateSegment(ctx context.Context, req SegmentRequest) (json.RawMessage, error)

	// StreamContent starts an emit_campaign_content generation.
	// Errors at start: ErrUnavailable, ctx errors.
	StreamContent(ctx context.Context, req ContentRequest) (ContentStream, error)
}

type Repair struct {
	Output json.RawMessage           // previous raw tool input (may be invalid JSON)
	Errors []validation.FieldError   // why it was rejected
}

type SegmentRequest struct {
	Brief    string
	Attempt  int     // 1 or 2
	Previous *Repair // non-nil iff Attempt == 2
}

type ContentRequest struct {
	Brief    string
	Rule     json.RawMessage  // stored segment rule, for context
	Longest  content.Longest  // current segment's longest values; told to the model as length budget
	Attempt  int
	Previous *Repair
}

type ContentChunk struct {
	Path string // "sms.body" | "email.subject" | "email.body"
	Text string // non-empty
}

type ContentStream interface {
	// Recv returns the next chunk; io.EOF when the model finished normally WITH a tool_use block;
	// ErrNoToolCall when the response ended without any emit_campaign_content tool_use block
	// (text only); ErrTruncated if the stream ended before the tool input was complete;
	// ErrUnavailable on transport failure; ctx errors on cancellation.
	Recv() (ContentChunk, error)
	// Raw returns the complete accumulated tool input. Meaningful only after Recv returned io.EOF.
	Raw() json.RawMessage
	Close() error
}

var (
	ErrUnavailable         = errors.New("llm: unavailable")
	ErrTruncated           = errors.New("llm: output truncated")
	ErrNoToolCall          = errors.New("llm: model did not call the tool")
	ErrInvalidMode         = errors.New("llm: invalid mode")
	ErrMissingAPIKey       = errors.New("llm: ANTHROPIC_API_KEY is required for LLM_PROVIDER=real")
	ErrRealClientForbidden = errors.New("llm: real client is forbidden in tests")
	ErrUnknownScenario     = errors.New("llm: unknown mock scenario")
	ErrUnknownTool         = errors.New("llm: unknown tool")
)

// ---- Mock ----

type Scenario string

const (
	ScenarioOK                Scenario = "ok"
	ScenarioInvalidJSON       Scenario = "invalid_json"
	ScenarioSchemaViolation   Scenario = "schema_violation"
	ScenarioOverLimitSMS      Scenario = "over_limit_sms"
	ScenarioOverLimitSubject  Scenario = "over_limit_subject"
	ScenarioUnknownVariable   Scenario = "unknown_variable"
	ScenarioInjectionAttempt  Scenario = "injection_attempt"
	ScenarioTruncatedStream   Scenario = "truncated_stream"
	ScenarioThenOKAfterRepair Scenario = "then_ok_after_repair"
	ScenarioAlwaysInvalid     Scenario = "always_invalid"
	ScenarioSQLInValue        Scenario = "sql_in_value"
	ScenarioHTMLInContent     Scenario = "html_in_content"
	ScenarioLLMUnavailable    Scenario = "llm_unavailable"
	ScenarioStalledStream     Scenario = "stalled_stream"
)

func Scenarios() []Scenario                    // the 14 above, in this order
func ParseScenario(s string) (Scenario, error) // ErrUnknownScenario

// WithScenario overrides the scenario for calls made with the returned context
// (the HTTP layer maps the X-Mock-LLM-Scenario header to this).
func WithScenario(ctx context.Context, s Scenario) context.Context
func ScenarioFromContext(ctx context.Context) (Scenario, bool)

type MockOptions struct {
	DefaultScenario Scenario      // zero => ScenarioOK
	ChunkSize       int           // runes per chunk; zero => 20
	ChunkDelay      time.Duration // delay between chunks via Clock; zero => none
	Clock           clock.Clock   // nil => clock.Real()
}

type MockCall struct {
	Operation string   // "segment" | "content"
	Scenario  Scenario
	Attempt   int
}

// Mock implements Client deterministically per llm-tools.md §3. Safe for concurrent use.
type Mock struct{ /* unexported */ }

func NewMock(opts MockOptions) *Mock
func (m *Mock) GenerateSegment(ctx context.Context, req SegmentRequest) (json.RawMessage, error)
func (m *Mock) StreamContent(ctx context.Context, req ContentRequest) (ContentStream, error)
func (m *Mock) Calls() []MockCall // in call order
```

## `internal/composer`

```go
package composer

// ErrInvalidOutput: the model output was still invalid after the single repair attempt.
// The returned error also unwraps to the last attempt's *validation.Error.
var ErrInvalidOutput = errors.New("composer: llm output invalid after repair")

type SegmentOutcome struct {
	Rule     segment.Rule
	Attempts int // 1 or 2
}

// GenerateSegment runs attempt 1, validates (segment.Parse + segment.Validate), and on failure
// runs exactly one repair attempt (llm-tools.md §2).
// Errors: ErrInvalidOutput (wrapping *validation.Error), llm.ErrUnavailable, ctx errors.
// Mapping of client errors to field errors (path ""): llm.ErrTruncated => "truncated_output",
// llm.ErrNoToolCall => "no_tool_call". Both trigger the repair attempt like any invalid output.
func GenerateSegment(ctx context.Context, c llm.Client, brief string) (SegmentOutcome, error)

// Event is one content-stream SSE event (sse-events.md §1). Name() is the SSE event name.
type Event interface{ Name() string }

type DeltaEvent struct {
	Attempt int    `json:"attempt"`
	Channel string `json:"channel"` // "sms" | "email"
	Field   string `json:"field"`   // "body" | "subject"
	Text    string `json:"text"`
}
type InvalidEvent struct {
	Attempt          int                     `json:"attempt"`
	Final            bool                    `json:"final"`
	Errors           []validation.FieldError `json:"errors"`
	ContentVersionID *string                 `json:"content_version_id"`
}
type ValidatedEvent struct {
	Attempt          int               `json:"attempt"`
	ContentVersionID string            `json:"content_version_id"`
	VersionNumber    int               `json:"version_number"`
	Content          content.Content   `json:"content"`
	WorstCase        content.WorstCase `json:"worst_case"`
}

func (DeltaEvent) Name() string     // "delta"
func (InvalidEvent) Name() string   // "invalid"
func (ValidatedEvent) Name() string // "validated"

type ContentOutcome struct {
	Valid     bool
	Content   content.Content         // set when Valid or Storable
	WorstCase *content.WorstCase      // set when the template syntax is valid
	Errors    []validation.FieldError // last attempt's errors when !Valid
	// Storable: !Valid, content.Parse succeeded (all keys present, right types, no extra keys) and
	// every error is a content-rule error (required for an EMPTY value, malformed_variable,
	// unknown_variable, sms_too_long, subject_too_long, email_body_too_long) => stored as status "invalid".
	Storable bool
	Attempts int
}

// ContentSession is an opened generation; attempt 1's stream is already connected.
type ContentSession struct{ /* unexported */ }

// OpenContent starts attempt 1. req.Longest is used both as model hint and for content.Validate.
// Errors: llm.ErrUnavailable, ctx errors (=> HTTP 502 / no stream).
func OpenContent(ctx context.Context, c llm.Client, req llm.ContentRequest) (*ContentSession, error)

// Run consumes the stream(s), calling emit for every DeltaEvent and for the non-final
// InvalidEvent (attempt 1 failure), then performs the repair attempt if needed. Validation =
// content.Parse + content.Validate(c, req.Longest) — identical to the edit path.
// Recv errors: llm.ErrTruncated => "truncated_output", llm.ErrNoToolCall => "no_tool_call"; both are
// invalid output and trigger the single repair attempt. llm.ErrUnavailable at ANY point after the
// stream opened (attempt 1 or 2) => no repair attempt; outcome !Valid, !Storable, error code
// "llm_unavailable" (path "") => caller emits the final invalid; campaign state unchanged.
// It NEVER emits the terminal event; the caller persists and emits ValidatedEvent or
// InvalidEvent{Final:true}. Returns ctx errors on cancellation (caller stores nothing),
// or the error returned by emit.
func (s *ContentSession) Run(emit func(Event) error) (ContentOutcome, error)
```

## `internal/campaign`

```go
package campaign

type State string

const (
	StateDraft         State = "draft"
	StateAudienceReady State = "audience_ready"
	StateContentReady  State = "content_ready"
	StateApproved      State = "approved"
	StateSending       State = "sending"
	StateCompleted     State = "completed"
)

type Event string

const (
	EventSegmentGenerated   Event = "segment_generated"   // valid rule stored
	EventContentValidated   Event = "content_validated"   // new validated version (LLM or edit)
	EventContentInvalid     Event = "content_invalid"     // invalid LLM draft stored (failed (re)generation)
	EventApproved           Event = "approved"
	EventSendAccepted       Event = "send_accepted"
	EventDeliveriesFinished Event = "deliveries_finished" // no queued/sending delivery left
)

// Transition is the pure state machine (requirements "Kampanya durum makinesi").
//   segment_generated:   draft|audience_ready|content_ready|approved -> audience_ready
//   content_validated:   audience_ready|content_ready|approved      -> content_ready
//   content_invalid:     audience_ready|content_ready|approved      -> unchanged (same state; VARSAYIM:
//                        a failed regeneration does not revoke an approval)
//   approved:            content_ready|approved -> approved
//   send_accepted:       approved -> sending
//   deliveries_finished: sending -> completed
// Any other pair => ErrInvalidState.
func Transition(from State, ev Event) (State, error)

const BriefMaxRunes = 2000

// MaxGenerationDuration bounds one segment or content generation (VARSAYIM). The per-campaign
// generation lock is released when the request context is cancelled (client disconnect) or, at the
// latest, after this duration; a content stream still running then ends with the final invalid
// event, code "llm_unavailable". While the lock is held, GenerateSegment, StartContentGeneration,
// EditContent, Approve and Send return ErrGenerationInProgress (Send is included so that a
// persisting content generation can never meet a campaign already in state `sending`).
const MaxGenerationDuration = 120 * time.Second

// NormalizeBrief NFC-normalizes and trims s. Errors (*validation.Error, path "brief"):
// empty => code "required" (matches validation.ErrRequired); > 2000 runes => code "too_long"
// (matches validation.ErrTooLong).
func NormalizeBrief(s string) (string, error)

var (
	ErrCampaignNotFound       = errors.New("campaign: not found")                 // 404 campaign_not_found
	ErrContentVersionNotFound = errors.New("campaign: content version not found") // 404 content_version_not_found
	ErrCustomerNotFound       = errors.New("campaign: customer not in audience")  // 404 customer_not_found
	ErrInvalidState           = errors.New("campaign: invalid state")             // 409 invalid_state
	ErrContentNotValidated    = errors.New("campaign: content not validated")     // 409 content_not_validated
	ErrStaleContentVersion    = errors.New("campaign: stale content version")     // 409 stale_content_version
	ErrGenerationInProgress   = errors.New("campaign: generation in progress")    // 409 generation_in_progress
)

// ---- Domain structs: JSON encoding of each is EXACTLY the OpenAPI schema of the same name ----

type ContentStatus string

const (
	ContentValidated ContentStatus = "validated"
	ContentInvalid   ContentStatus = "invalid"
)

type ContentSource string

const (
	SourceLLM  ContentSource = "llm"
	SourceEdit ContentSource = "edit"
)

type Segment struct {
	Rule          json.RawMessage  `json:"rule"` // root group
	Audience      segment.Audience `json:"audience"`
	LongestValues content.Longest  `json:"longest_values"`
	ReferenceTime time.Time        `json:"reference_time"`
	Revision      int              `json:"revision"`
	LLMAttempts   int              `json:"llm_attempts"`
	GeneratedAt   time.Time        `json:"generated_at"`
}

type ContentVersion struct {
	ID               string                  `json:"id"`
	CampaignID       string                  `json:"campaign_id"`
	VersionNumber    int                     `json:"version_number"`
	SegmentRevision  int                     `json:"segment_revision"`
	Status           ContentStatus           `json:"status"`
	Source           ContentSource           `json:"source"`
	Content          content.Content         `json:"content"`
	WorstCase        *content.WorstCase      `json:"worst_case"` // null iff template syntax invalid
	ValidationErrors []validation.FieldError `json:"validation_errors"`
	LLMAttempts      *int                    `json:"llm_attempts"`
	CreatedAt        time.Time               `json:"created_at"`
}

type Approval struct {
	ID               string    `json:"id"`
	ContentVersionID string    `json:"content_version_id"`
	ApprovedAt       time.Time `json:"approved_at"`
}

type Campaign struct {
	ID                   string          `json:"id"`
	Brief                string          `json:"brief"`
	State                State           `json:"state"`
	Segment              *Segment        `json:"segment"`
	LatestContentVersion *ContentVersion `json:"latest_content_version"` // latest VALIDATED, current revision
	Approval             *Approval       `json:"approval"`
	SendingStartedAt     *time.Time      `json:"sending_started_at"`
	CompletedAt          *time.Time      `json:"completed_at"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type CampaignPage struct {
	Items  []Campaign `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

type SegmentResult struct {
	CampaignID string  `json:"campaign_id"`
	State      State   `json:"state"`
	Segment    Segment `json:"segment"`
}

type SegmentCount struct {
	Audience      segment.Audience `json:"audience"`
	ReferenceTime time.Time        `json:"reference_time"`
}

type DeliveryTotals struct {
	Total int `json:"total"`
	SMS   int `json:"sms"`
	Email int `json:"email"`
}

type SendResult struct {
	CampaignID       string         `json:"campaign_id"`
	State            State          `json:"state"` // sending | completed
	ContentVersionID string         `json:"content_version_id"`
	Deliveries       DeliveryTotals `json:"deliveries"`
}

type StatusCounts struct {
	Total   int `json:"total"`
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
}

type ChannelCounts struct {
	SMS   StatusCounts `json:"sms"`
	Email StatusCounts `json:"email"`
}

type CampaignStatus struct {
	CampaignID       string        `json:"campaign_id"`
	State            State         `json:"state"`
	Counts           StatusCounts  `json:"counts"`
	ByChannel        ChannelCounts `json:"by_channel"`
	SendingStartedAt *time.Time    `json:"sending_started_at"`
	CompletedAt      *time.Time    `json:"completed_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type Delivery struct {
	ID                string     `json:"id"`
	CampaignID        string     `json:"campaign_id"`
	ContentVersionID  string     `json:"content_version_id"`
	CustomerID        string     `json:"customer_id"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	FailedAttempts    int        `json:"failed_attempts"`
	RateLimitedCount  int        `json:"rate_limited_count"`
	RetryCount        int        `json:"retry_count"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	FirstAttemptAt    *time.Time `json:"first_attempt_at"`
	LastAttemptAt     *time.Time `json:"last_attempt_at"`
	LastHTTPStatus    *int       `json:"last_http_status"`
	LastError         *string    `json:"last_error"`
	FailureReason     *string    `json:"failure_reason"`
	ProviderMessageID *string    `json:"provider_message_id"`
	SentAt            *time.Time `json:"sent_at"`
	FailedAt          *time.Time `json:"failed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type DeliveryPage struct {
	Items  []Delivery `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// DeliveryFilter: empty string = no filter. Order: "customer" (default) | "recent".
type DeliveryFilter struct {
	Status, Channel, CustomerID, Order string
	Limit, Offset                      int
}

// PreviewQuery: CustomerID wins over SampleIndex when both are set.
type PreviewQuery struct {
	CustomerID       string
	ContentVersionID string
	SampleIndex      int
}

type PreviewCustomer struct {
	ID        string `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	City      string `json:"city"`
}
type PreviewSMS struct {
	Text        string `json:"text"`
	Runes       int    `json:"runes"`
	Limit       int    `json:"limit"`
	WithinLimit bool   `json:"within_limit"`
}
type PreviewEmail struct {
	Subject      string `json:"subject"`
	SubjectRunes int    `json:"subject_runes"`
	SubjectLimit int    `json:"subject_limit"`
	WithinLimit  bool   `json:"within_limit"`
	Text         string `json:"text"`
	HTML         string `json:"html"`
}
type Preview struct {
	CampaignID       string          `json:"campaign_id"`
	ContentVersionID string          `json:"content_version_id"`
	SampleIndex      int             `json:"sample_index"`
	SampleCount      int             `json:"sample_count"`
	Customer         PreviewCustomer `json:"customer"`
	SMS              PreviewSMS      `json:"sms"`
	Email            PreviewEmail    `json:"email"`
}

// ---- Use cases ----

// Service is the use-case layer behind internal/httpapi; httpapi handler tests use a fake Service.
type Service interface {
	Create(ctx context.Context, brief string) (Campaign, error)
	Get(ctx context.Context, id string) (Campaign, error)
	List(ctx context.Context, limit, offset int) (CampaignPage, error)
	GenerateSegment(ctx context.Context, id string) (SegmentResult, error)
	// StartContentGeneration performs every pre-stream check and connects LLM attempt 1. Errors here
	// become JSON responses; after it returns a run, the handler writes HTTP 200 + SSE.
	// Check order (first failure wins): ErrCampaignNotFound -> ErrInvalidState (draft, sending,
	// completed) -> ErrGenerationInProgress -> llm.ErrUnavailable. (The handler already answered
	// 404 for a malformed id, 422 for an unknown X-Mock-LLM-Scenario and 400/413/422 for the body,
	// in that order, before calling the Service: README §5.4.) Acquires the generation lock, which
	// the returned run releases when Stream returns (any outcome) or when ctx is cancelled.
	StartContentGeneration(ctx context.Context, id string) (ContentRun, error)
	// EditContent: ErrCampaignNotFound -> ErrGenerationInProgress -> ErrInvalidState (draft, sending,
	// completed) -> content.Parse + content.Validate against the CURRENT segment's longest values
	// (*validation.Error => 422, nothing stored). Success, in ONE transaction under the campaign row
	// lock: next version_number, status validated, source edit, segment_revision = current, state
	// content_ready (EventContentValidated), active approval revoked ("content_changed"). No
	// de-duplication: saving text identical to the latest version still creates a new version.
	EditContent(ctx context.Context, id string, raw []byte) (ContentVersion, error)
	ListContentVersions(ctx context.Context, id string) ([]ContentVersion, error)
	GetContentVersion(ctx context.Context, id, versionID string) (ContentVersion, error)
	// Approve: openapi approveContent "Checks, in this order". Idempotent for the already approved
	// version (returns the unchanged Campaign; no new approvals row, approved_at unchanged, the
	// worst-case re-check is NOT run again).
	Approve(ctx context.Context, id, versionID string) (Campaign, error)
	// Send: ErrCampaignNotFound -> ErrContentVersionNotFound -> ErrGenerationInProgress ->
	// ErrInvalidState (not approved / second send) -> ErrStaleContentVersion (README §5.4).
	Send(ctx context.Context, id, versionID string) (SendResult, error)
	Status(ctx context.Context, id string) (CampaignStatus, error)
	Preview(ctx context.Context, id string, q PreviewQuery) (Preview, error)
	ListDeliveries(ctx context.Context, id string, f DeliveryFilter) (DeliveryPage, error)
	CountRule(ctx context.Context, raw []byte) (SegmentCount, error)
}

// ContentRun is a started generation. Stream calls emit for every composer event in order and
// finally exactly once with the terminal event (ValidatedEvent or InvalidEvent{Final:true}),
// persisting the version before emitting it. Persistence is ONE transaction under the campaign row
// lock: validated => next version_number, state content_ready (EventContentValidated), active
// approval revoked ("content_changed"); storable final invalid => the draft is stored with status
// invalid and nothing else changes (EventContentInvalid); any other final invalid stores nothing.
// ctx is checked before that transaction starts: if it is already cancelled (client gone) nothing is
// stored and Stream returns ctx.Err(); a transaction that has started is completed even if the
// client disconnects while the terminal event is being written (a version the client never saw may
// exist; it is visible through GET /api/campaigns/{id}). Fakes: any type with this method (e.g.
// replaying a fixed []composer.Event).
type ContentRun interface {
	Stream(ctx context.Context, emit func(composer.Event) error) error
}

// Store is the persistence port used by the service. Its method set is owned by the
// implementation and is NOT a test seam (black-box tests do not fake it); it is constructed only
// by postgres.NewCampaignStore.
type Store interface{ /* implementation-defined */ }

type Deps struct {
	Store    Store
	Audience segment.Counter
	LLM      llm.Client
	Clock    clock.Clock
	// SegmentReferenceTime overrides Clock.Now() as the segment reference time when non-nil.
	SegmentReferenceTime *time.Time
	Logger               *slog.Logger
}

func NewService(d Deps) Service
```

`campaign.Transition` ve `campaign.NormalizeBrief` saf fonksiyon olarak unit test edilir. Servis davranışının kendisi HTTP + gerçek DB üzerinden (entegrasyon/E2E) test edilir; HTTP handler'ları ise sahte `campaign.Service` + sahte `ContentRun` + `clock.Fake` ile unit test edilir (bkz. §httpapi).

Hata eşlemesi (servis → HTTP) [README §5](README.md#5-hata-gövdesi-ve-kodlar)'tedir. `*validation.Error` → 422 `validation_failed`; `composer.ErrInvalidOutput` → 502 `llm_invalid_output`; `llm.ErrUnavailable` → 502 `llm_unavailable`.

## `internal/delivery`

```go
package delivery

type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
)

type FailureReason string

const (
	FailureProviderRejected     FailureReason = "provider_rejected"           // non-retryable 4xx / 1xx / 3xx
	FailureMaxAttempts          FailureReason = "max_attempts_exceeded"       // 5th failed attempt
	FailureRateLimitBudget      FailureReason = "rate_limit_budget_exhausted" // 20th 429 or 10-minute window
	FailureContentTooLong       FailureReason = "content_too_long"            // send-time render check
	FailureRecipientUnavailable FailureReason = "recipient_unavailable"       // contact/consent gone at send time
)

// Advisory lock keys (pg_try_advisory_lock), one dispatcher per channel.
const (
	LockKeySMS   int64 = 7001
	LockKeyEmail int64 = 7002
)

// ---- Retry policy (REQ-SND-04, PLAN §0.4) ----

type RetryConfig struct {
	MaxAttempts     int           // 5: failed attempts incl. the first; the 5th failure => failed
	BaseDelay       time.Duration // 1s
	MaxDelay        time.Duration // 60s
	MaxRateLimited  int           // 20: the 20th 429 => failed
	RateLimitWindow time.Duration // 10m, measured from FirstAttemptAt
}

func DefaultRetryConfig() RetryConfig

// Attempt describes one finished provider request.
type Attempt struct {
	StatusCode     int           // 0 when Err != nil
	RetryAfter     time.Duration // parsed Retry-After (see provider.ParseRetryAfter)
	HasRetryAfter  bool
	Err            error         // transport error: provider.ErrTimeout / provider.ErrConnection
	FailedAttempts int           // failed (non-429) attempts BEFORE this one
	RateLimited    int           // 429 responses BEFORE this one
	FirstAttemptAt time.Time
	Now            time.Time
}

type Action int

const (
	ActionMarkSent Action = iota + 1
	ActionRetry
	ActionFail
)

// CountKind tells which per-delivery counter a provider response increments.
// (Named CountKind to avoid confusion with segment.Counter.)
type CountKind int

const (
	CountNone        CountKind = iota // 2xx, other 4xx
	CountFailure                    // 5xx, 408, timeout, connection error => failed_attempts+1
	CountRateLimited                // 429 => rate_limited_count+1
)

type Decision struct {
	Action        Action
	Count         CountKind     // which counter this response increments (also when Action == ActionFail)
	Delay         time.Duration // ActionRetry only
	NextAttemptAt time.Time     // ActionRetry only: Now + Delay
	FailureReason FailureReason // ActionFail only
}

type RetryPolicy struct{ /* unexported; safe for concurrent use */ }

// NewRetryPolicy uses rng exclusively for jitter (deterministic for a fixed seed and call order).
func NewRetryPolicy(cfg RetryConfig, rng *rand.Rand) *RetryPolicy

// Next decides what happens after a provider response. Normative rules:
//  2xx                          -> ActionMarkSent, CountNone
//  429                          -> n := RateLimited+1; CountRateLimited
//                                  fail(rate_limit_budget_exhausted) if n >= MaxRateLimited
//                                     or Now-FirstAttemptAt >= RateLimitWindow
//                                  delay := RetryAfter if HasRetryAfter (negative => 0) else Jitter(n)
//                                  fail(rate_limit_budget_exhausted) if Now+delay > FirstAttemptAt+RateLimitWindow
//                                  else ActionRetry(delay)
//  5xx, 408, Err != nil         -> n := FailedAttempts+1; CountFailure
//                                  fail(max_attempts_exceeded) if n >= MaxAttempts else ActionRetry(Jitter(n))
//  any other status             -> ActionFail(provider_rejected), CountNone
func (p *RetryPolicy) Next(a Attempt) Decision

// Jitter returns full jitter for the n-th (1-based) retry. Exact draw (one rng call per Jitter call):
//   cap := min(MaxDelay, BaseDelay << (n-1))          // n=1 => 1s, 2 => 2s, 3 => 4s, 4 => 8s, n>=7 => 60s
//   return time.Duration(rng.Int64N(int64(cap)))      // uniform in [0, cap), nanosecond resolution
// Next draws from rng ONLY via Jitter (i.e. never for 2xx, other 4xx, or a 429 with Retry-After), so a
// test with rand.New(rand.NewPCG(s1, s2)) can compute the exact delays by replaying the same draws.
func (p *RetryPolicy) Jitter(n int) time.Duration

// ---- Rate limiter (REQ-SND-02) ----

// RateLimiter gates provider request STARTS for one channel.
type RateLimiter interface {
	Wait(ctx context.Context) error // returns nil when the caller may start one request; ctx.Err() otherwise
}

// NewTokenBucket returns a burst-1 limiter: consecutive permits are at least interval apart on c
// (interval = time.Second / DISPATCH_RATE_PER_SECOND = 10ms). The first permit is immediate.
// Guarantee: in every half-open window [t, t+1s) at most 1s/interval permits are granted.
// Safe for concurrent use; waiting uses c.After (fake-clock friendly).
func NewTokenBucket(c clock.Clock, interval time.Duration) *TokenBucket

type TokenBucket struct{ /* unexported */ }

func (b *TokenBucket) Wait(ctx context.Context) error

// ---- Queue store (implemented by postgres.NewDeliveryStore; fakeable) ----

type Job struct {
	ID             string // delivery id == Idempotency-Key
	CampaignID     string
	CustomerID     string
	Channel        Channel
	To             string // CURRENT phone (sms) or email (email) re-read from customers at claim time; "" if NULL. NEVER logged
	Consent        bool   // CURRENT channel opt-in (sms_opt_in / email_opt_in) re-read at claim time
	Recipient      content.Recipient
	Content        content.Content // approved version template
	Attempts       int             // after this claim
	FailedAttempts int
	RateLimited    int
	FirstAttemptAt time.Time
}

type SentUpdate struct {
	At                time.Time
	HTTPStatus        int
	ProviderMessageID string
}
type RetryUpdate struct {
	At            time.Time
	NextAttemptAt time.Time
	Count         CountKind
	HTTPStatus    int    // 0 for transport errors
	LastError     string // see LastError format below
}
type FailedUpdate struct {
	At         time.Time
	Reason     FailureReason
	Count      CountKind
	HTTPStatus int
	LastError  string
}

type Store interface {
	// TryLockChannel takes the channel's advisory lock on a dedicated connection.
	TryLockChannel(ctx context.Context, ch Channel) (release func(), acquired bool, err error)
	// Claim moves up to limit due queued rows (next_attempt_at <= now) to sending with
	// lease_expires_at = leaseUntil, using FOR UPDATE SKIP LOCKED.
	Claim(ctx context.Context, ch Channel, now time.Time, limit int, leaseUntil time.Time) ([]Job, error)
	// ReclaimExpired returns sending rows whose lease expired before now to queued.
	ReclaimExpired(ctx context.Context, ch Channel, now time.Time) (int, error)
	// Mark* update a row currently in sending; ErrLeaseLost if it is not. Each also completes
	// the campaign in the same transaction when no queued/sending delivery remains.
	MarkSent(ctx context.Context, id string, u SentUpdate) error
	ScheduleRetry(ctx context.Context, id string, u RetryUpdate) error
	MarkFailed(ctx context.Context, id string, u FailedUpdate) error
}

var ErrLeaseLost = errors.New("delivery: lease lost")

// LastError format (deliveries.last_error, RetryUpdate/FailedUpdate.LastError), closed set:
//   "http_<status>" for any HTTP response that is not 2xx (e.g. "http_408", "http_429", "http_500",
//   "http_503", "http_400"), "timeout" (provider.ErrTimeout), "connection_error" (provider.ErrConnection),
//   "content_too_long", "recipient_unavailable".
// Duplicate delivery rows are prevented by the DB constraint deliveries_campaign_channel_customer_key
// (SQLSTATE 23505); there is no Go sentinel for it.

// ---- Dispatcher ----

type DispatcherConfig struct {
	Channel           Channel
	BatchSize         int           // 50
	Concurrency       int           // 32 in-flight requests max
	PollInterval      time.Duration // 200ms when nothing is due
	Lease             time.Duration // 60s
	LockRetryInterval time.Duration // 1s between lock attempts when not leader
}

type DispatcherDeps struct {
	Store    Store
	Provider provider.Client
	// Limiter gates request starts. The per-channel limit holds across dispatchers only if they
	// SHARE one RateLimiter (unit tests) or if only one dispatcher sends (production: advisory lock).
	Limiter  RateLimiter
	Policy   *RetryPolicy
	Clock    clock.Clock
	Logger   *slog.Logger
	Recorder Recorder // optional (nil => no metrics); *metrics.WorkerMetrics implements it
}

// Recorder receives dispatcher observations (worker /metrics, metrics.md §2).
type Recorder interface {
	SetLeader(channel string, leader bool)                                   // scc_dispatcher_leader{channel}
	ObserveProviderResponse(channel string, code string, d time.Duration)   // scc_provider_requests_total, duration
	ObserveLimiterWait(channel string, d time.Duration)                      // scc_rate_limiter_wait_seconds
}

type Dispatcher struct{ /* unexported */ }

func NewDispatcher(cfg DispatcherConfig, deps DispatcherDeps) (*Dispatcher, error) // ErrInvalidConfig

var ErrInvalidConfig = errors.New("delivery: invalid dispatcher config")

// Run loops until ctx is done (returns ctx.Err()): acquires the channel lock (only the holder
// sends), reclaims expired leases, claims due jobs and processes them.
func (d *Dispatcher) Run(ctx context.Context) error

// Leader reports whether this dispatcher currently holds the channel's advisory lock (also
// published as Recorder.SetLeader / scc_dispatcher_leader{channel}). Only the leader calls the provider.
func (d *Dispatcher) Leader() bool

// ProcessOnce claims one batch at Clock.Now() and processes it to completion WITHOUT taking the
// channel lock (test seam). Unit tests that run several dispatchers/ProcessOnce loops concurrently for
// one channel MUST pass the same RateLimiter instance to all of them. For each job, in this order:
//   1. !job.Consent || job.To == ""  => MarkFailed(recipient_unavailable, LastError "recipient_unavailable"),
//      NO provider call, NO limiter permit (REQ-AUD-04 at send time);
//   2. content.RenderContent + content.CheckRendered fails => MarkFailed(content_too_long,
//      LastError "content_too_long"), NO provider call, NO limiter permit (REQ-CNT-05);
//   3. Limiter.Wait -> Provider.Send -> Policy.Next -> Store.MarkSent / ScheduleRetry / MarkFailed.
// Returns the number of jobs processed.
func (d *Dispatcher) ProcessOnce(ctx context.Context) (int, error)
```

Teslimat log kayıtları (REQ-SEC-01), `Logger` üzerinden, JSON handler'da:

| `msg` | Seviye | Öznitelikler |
|---|---|---|
| `delivery sent` | INFO | `delivery_id`, `campaign_id`, `customer_id`, `channel`, `attempt`, `http_status` |
| `delivery retry scheduled` | WARN | yukarıdakiler + `next_attempt_at`, `reason` (= `LastError` biçimi: `http_<status>`, `timeout`, `connection_error`) |
| `delivery failed` | ERROR | yukarıdakiler + `failure_reason` |

Hiçbir delivery log kaydı alıcı telefonunu, e-postasını, adını veya mesaj gövdesini içermez (`to`, `phone`, `email`, `body` anahtarı yok; değerlerde `@example.` ve `+90500000` deseni geçmez).

## `internal/provider`

Protokol: [`mock-provider.md`](mock-provider.md).

```go
package provider

type Channel string // "sms" | "email" (own type: provider must not import delivery)

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

type Message struct {
	DeliveryID string            // sent as Idempotency-Key
	Channel    Channel
	To         string
	SMSBody    string            // sms
	Subject    string            // email
	Text       string            // email plain text
	HTML       string            // email html (content.RenderEmailHTML)
}

type Response struct {
	StatusCode        int
	RetryAfter        time.Duration
	HasRetryAfter     bool
	ProviderMessageID string // from body "id" on 2xx
	Replayed          bool   // response header Idempotent-Replayed: true
}

// Client sends one message. Any HTTP response (any status) is returned with err == nil.
// err != nil only for transport failures: ErrTimeout (deadline/timeout) or ErrConnection
// (refused, reset, EOF before response — includes "response lost").
type Client interface {
	Send(ctx context.Context, m Message) (Response, error)
}

func NewHTTPClient(baseURL string, hc *http.Client, c clock.Clock) Client

var (
	ErrTimeout    = errors.New("provider: timeout")
	ErrConnection = errors.New("provider: connection error")
)

// ParseRetryAfter parses delay-seconds ("2") or an HTTP-date relative to now.
// Returns (0,false) for empty or unparsable values; past dates => (0,true).
func ParseRetryAfter(v string, now time.Time) (time.Duration, bool)
```

İmport yönü: `delivery` → `provider`, `content`, `clock`; `provider` hiçbir domain paketini import etmez.

## `internal/mockprovider`

```go
package mockprovider

type Config struct {
	Addr              string        // MOCK_PROVIDER_ADDR, default ":8090"
	Seed              int64         // MOCK_PROVIDER_SEED, default 1
	ErrorRate         float64       // MOCK_PROVIDER_ERROR_RATE, default 0.05
	RateLimitRate     float64       // MOCK_PROVIDER_RATE_LIMIT_RATE, default 0.02
	LostResponseRate  float64       // MOCK_PROVIDER_LOST_RESPONSE_RATE, default 0
	LatencyMin        time.Duration // MOCK_PROVIDER_LATENCY_MIN, default 20ms
	LatencyMax        time.Duration // MOCK_PROVIDER_LATENCY_MAX, default 200ms
	RateLimitPerSecond int          // MOCK_PROVIDER_RATE_LIMIT_PER_SECOND, default 100 (per channel)
	RetryAfterSeconds int           // MOCK_PROVIDER_RETRY_AFTER_SECONDS, default 1
	AdminEnabled      bool          // MOCK_PROVIDER_ADMIN, default false
}

func LoadConfig(getenv func(string) string) (Config, error) // ErrInvalidConfig

var ErrInvalidConfig = errors.New("mockprovider: invalid config")

// New returns the HTTP handler of mock-provider.md. Randomness comes only from cfg.Seed;
// latency waits use c.
func New(cfg Config, c clock.Clock) http.Handler
```

## `internal/seed`

Ayrıntı: [`seed.md`](seed.md).

```go
package seed

const MinUsers = 200

type Options struct {
	Seed    int64
	Users   int
	RefTime time.Time // UTC, second precision
}

// Generate is pure and deterministic: equal Options => byte-identical WriteJSONL output and manifest.
// Errors: ErrTooFewUsers (< MinUsers), ErrInvalidRefTime (zero time).
func Generate(opts Options) (*Dataset, error)

type Dataset struct{ /* unexported */ }

func (d *Dataset) WriteJSONL(w io.Writer) error // customers then events, each sorted by id, one JSON object per line
func (d *Dataset) Manifest() Manifest

// Manifest mirrors manifest.schema in seed.md. MarshalCanonical: 2-space indent, keys in the
// order defined in seed.md, trailing newline.
type Manifest struct{ /* fields per seed.md */ }

func (m Manifest) MarshalCanonical() ([]byte, error)

// Load upserts the dataset in one transaction. If seed_runs holds a different dataset_sha256 and
// reset is false => ErrDatasetMismatch. reset deletes deliveries, approvals, content_versions,
// campaigns, events, customers (in that order) before inserting.
func Load(ctx context.Context, db *pgxpool.Pool, d *Dataset, reset bool) error

var (
	ErrTooFewUsers     = errors.New("seed: too few users")
	ErrInvalidRefTime  = errors.New("seed: invalid ref time")
	ErrDatasetMismatch = errors.New("seed: database holds a different dataset; use --reset")
)
```

## `internal/postgres`, `internal/migrate`

```go
package postgres

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)
func NewDeliveryStore(db *pgxpool.Pool) delivery.Store
func NewAudience(db *pgxpool.Pool) segment.Counter
// CampaignStore is the concrete persistence type for internal/campaign (implements campaign.Store).
type CampaignStore struct{ /* unexported */ }

func NewCampaignStore(db *pgxpool.Pool) *CampaignStore

package migrate

func Up(ctx context.Context, migratorURL secret.Secret) error   // applies all pending migrations (MIGRATE_DATABASE_URL)
func Down(ctx context.Context, migratorURL secret.Secret) error // reverts all
```

## `internal/sse`

```go
package sse

var ErrStreamingUnsupported = errors.New("sse: response writer cannot flush")

type Writer struct{ /* unexported */ }

// NewWriter sets the SSE headers (sse-events.md §0) and writes nothing else.
func NewWriter(w http.ResponseWriter) (*Writer, error)
func (w *Writer) Event(name string, data any) error            // "event: name\ndata: <json>\n\n" + flush
func (w *Writer) EventWithID(id int64, name string, data any) error
func (w *Writer) Comment(text string) error                    // ": text\n\n"
func (w *Writer) Retry(ms int) error                           // "retry: ms\n\n"
```

## `internal/health`, `internal/metrics`

```go
package health

type Pinger interface{ Ping(ctx context.Context) error }

// Healthz: always 200 {"status":"ok"}. Readyz: Ping with 1s timeout => 200 {"status":"ready",
// "checks":{"database":"ok"}} or 503 {"status":"not_ready","checks":{"database":"unavailable"}}.
func Healthz() http.Handler
func Readyz(db Pinger) http.Handler

package metrics

// DeliveryStat is one row of: SELECT channel, status, failure_reason, count(*), sum(retry_count) ... GROUP BY.
type DeliveryStat struct {
	Channel       string
	Status        string
	FailureReason string // "" unless failed
	Count         int64
	Retries       int64
}

type DeliveryStatsSource interface {
	DeliveryStats(ctx context.Context) ([]DeliveryStat, error)
	CampaignStates(ctx context.Context) (map[string]int64, error)
}

// NewAPIHandler serves /metrics for cmd/api (metrics.md §1); DB-derived series are collected at scrape time.
func NewAPIHandler(src DeliveryStatsSource) http.Handler
// NewWorkerHandler serves /metrics for cmd/worker (metrics.md §2).
func NewWorkerHandler() (http.Handler, *WorkerMetrics)

// WorkerMetrics implements delivery.Recorder.
type WorkerMetrics struct{ /* unexported */ }

func (m *WorkerMetrics) ObserveProviderResponse(channel string, code string, d time.Duration) // scc_provider_requests_total + duration
func (m *WorkerMetrics) ObserveLimiterWait(channel string, d time.Duration)                    // scc_rate_limiter_wait_seconds
func (m *WorkerMetrics) SetLeader(channel string, leader bool)
```

## `internal/httpapi`

```go
package httpapi

type Deps struct {
	Service     campaign.Service // fakeable
	LLMProvider llm.Provider     // effective provider for GET /api/config and header gating
	DB          health.Pinger
	Metrics     http.Handler
	Clock       clock.Clock      // all stream timing (poll, heartbeat) runs on this clock
	// StatusPollInterval: live-status stream poll period; zero => 1s.
	StatusPollInterval time.Duration
	// HeartbeatInterval: ": keepalive" comment period on both SSE streams; zero => 15s.
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
}

// New returns the full router of openapi.yaml (including /healthz, /readyz, /metrics,
// body limit 64 KiB, X-Request-ID, error mapping).
func New(d Deps) http.Handler

// Route is one method + path template the router registers. Template uses the OpenAPI
// `{name}` syntax exactly as the key under `paths:` in openapi.yaml (e.g.
// "/api/campaigns/{campaign_id}/segment"); Method is upper case ("GET", "POST").
type Route struct {
	Method, Template string
}

// Routes returns every route New registers, sorted by Template, then Method. It is a pure
// function of the code (independent of Deps: /metrics is listed even when Deps.Metrics is nil)
// and lists only explicitly registered methods (the implicit HEAD-on-GET fallback is not a route).
func Routes() []Route

// OpenAPI is the embedded copy of the contract (openapi.yaml + schemas/*.schema.json under
// internal/httpapi/openapi/, same relative layout as docs/contracts/). Byte-identical to
// docs/contracts/ by `make check-contract-copies`.
var OpenAPI embed.FS
```

### Rota tablosu ve sözleşme kapsamı (API ↔ doc sapma koruması, README §11)

- **Tek tablo:** `New` rotalarını `Routes()`'un döndürdüğü **aynı** paket düzeyi tablodan (ör. `[]struct{Route; handler func(*api) http.Handler}`) kaydeder; `New` içinde tablo dışı `handle(...)` çağrısı yoktur. Böylece `Routes()` router'dan sapamaz. Bir faz yeni uç nokta eklerken tabloya satır ekler ve aynı PR'da `openapi.yaml`'ı günceller.
- **`api-routes` (test yazarı, `test/contract`):** `httpapi.Routes()` ile `openapi.yaml`'ın `paths × method` kümesi **iki yönlü** eşit olmalıdır: her spec operasyonunun bir rotası, her rotanın bir spec operasyonu vardır. İki SSE operasyonu (`generateContent`, `streamCampaignEvents`) dahildir. Faz içinde henüz yazılmamış operasyonlar için tek izinli istisna `test/contract/allowlist.go`'daki faz başına liste (`operationId` → faz; ör. P3'te `generateContent` … `previewContent` "P4/P5"); listedeki bir operasyonun rotası **varsa** test yine başarısız olur (liste temizlenmeli). Faz kapısında (slice → main) liste o fazın operasyonlarını içeremez.
- **`operationId` kapsamı (test yazarı, E2E):** sıkı doğrulama açıkken (`OPENAPI_VALIDATE=strict`) E2E istemcisi her istek/yanıt çiftini spec'e karşı doğrular ve eşleşen `operationId`'yi kaydeder; paket sonunda (`TestMain`) **uygulanmış her operasyon en az bir kez çağrılmış** olmalıdır (aynı faz izin listesi hariç). Yanıt gövdesi her zaman doğrulanır (SSE hariç, `x-sse-events`); istek gövdesi yalnız yanıt 2xx ise doğrulanır (testlerin bilerek gönderdiği geçersiz istekler 4xx almalıdır; geçersiz isteğe 2xx bir hatadır). Operasyona ait olmayan router yanıtları (bilinmeyen yol 404, desteklenmeyen metot 405) `components.responses.RouteNotFound` / `MethodNotAllowed` şemasına karşı doğrudan doğrulanır.
- **Spec'in okunduğu yer:** testler doğruluk kaynağını diskten okur: `OPENAPI_SPEC_PATH` (test konteynerinde `/src/docs/contracts/openapi.yaml`; Dockerfile `test` aşaması kaynağı `/src` altına kopyalar), boşsa modül kökü (`go.mod`'u içeren dizin) + `docs/contracts/openapi.yaml`. Yükleme `libopenapi` ile `DocumentConfiguration{BasePath: <spec dizini>, AllowFileReferences: true}`; `schemas/*.schema.json` göreli `$ref`'leri bu dizine göre çözülür. `LocalFS` (`fs.FS`) modu kullanılmaz: libopenapi v0.41.3'te dış dosya içindeki `#/$defs/...` referanslarını çözemiyor (P3'te denendi). `httpapi.OpenAPI` (embed) okunacaksa önce `os.CopyFS(t.TempDir(), sub)` ile diske açılır, sonra aynı `BasePath` yolu.
- **`go:embed` kısıtı:** `go:embed` paket dizininin dışını (`../../docs`) gömemez; bu yüzden `internal/httpapi/openapi/` altında birebir kopya tutulur (`openapi.yaml`, `schemas/segment-rule.schema.json`, `schemas/content.schema.json`) ve `make check-contract-copies` bunları `cmp` ile `docs/contracts/` karşılıklarıyla karşılaştırır (`check-tool-schemas` deseni; `make lint` ikisini de çağırır). Kopya elle düzenlenmez; değişiklik önce `docs/contracts/`'ta yapılır, sonra kopyalanır.

Akış handler'larının normatif davranışı (unit test: sahte `Service`, `clock.Fake`, `httptest.ResponseRecorder`/`httptest.Server`):

- **`GET /api/campaigns/{id}/events`:** önce `Service.Status` çağrılır (hata → JSON 404 vb.); sonra `retry: 3000` ve `snapshot` yazılır. Ardından döngü: `Clock.After(StatusPollInterval)` her tetiklendiğinde `Service.Status` yeniden çağrılır; sonuç son gönderilenden `updated_at` dışında farklıysa `status` olayı yazılır; `state == completed` ise `completed` yazılır ve akış kapanır. `Clock.After(HeartbeatInterval)` ile yorum satırı. İstemci kopunca (`ctx.Done`) döngü biter. Sunucu tarafında `LISTEN/NOTIFY` kullanılmaz (VARSAYIM; tek mekanizma yoklama).
- **`POST /api/campaigns/{id}/content`:** `Service.StartContentGeneration` hata dönerse JSON hata; dönmezse HTTP 200 + SSE başlıkları ve `ContentRun.Stream(ctx, emit)`; her `composer.Event` `event: <Name()>` + JSON olarak yazılır. Heartbeat aynı `HeartbeatInterval` ile.
- `X-Mock-LLM-Scenario` başlığı yalnız `LLMProvider == mock` iken `llm.ParseScenario` ile okunur ve `llm.WithScenario` ile `ctx`'e konur.

## Sentinel hata özeti

| Paket | Sentinel'ler |
|---|---|
| `validation` | `ErrInvalidJSON`, `ErrSchemaViolation`, `ErrRequired`, `ErrTooLong`, `ErrInvalidValue` |
| `config` | `ErrInvalidValue`, `ErrMissing` |
| `segment` | `ErrUnknownField`, `ErrUnknownOperator`, `ErrOperatorNotAllowed`, `ErrTypeMismatch`, `ErrEmptyGroup`, `ErrMaxDepthExceeded`, `ErrMaxConditionsExceeded` |
| `content` | `ErrSMSTooLong`, `ErrSubjectTooLong`, `ErrEmailBodyTooLong`, `ErrUnknownVariable`, `ErrMalformedVariable`, `ErrWorstCaseTooLong` |
| `llm` | `ErrUnavailable`, `ErrTruncated`, `ErrNoToolCall`, `ErrInvalidMode`, `ErrMissingAPIKey`, `ErrRealClientForbidden`, `ErrUnknownScenario`, `ErrUnknownTool` |
| `composer` | `ErrInvalidOutput` |
| `campaign` | `ErrCampaignNotFound`, `ErrContentVersionNotFound`, `ErrCustomerNotFound`, `ErrInvalidState`, `ErrContentNotValidated`, `ErrStaleContentVersion`, `ErrGenerationInProgress` |
| `delivery` | `ErrLeaseLost`, `ErrInvalidConfig` |
| `provider` | `ErrTimeout`, `ErrConnection` |
| `mockprovider` | `ErrInvalidConfig` |
| `seed` | `ErrTooFewUsers`, `ErrInvalidRefTime`, `ErrDatasetMismatch` |
| `sse` | `ErrStreamingUnsupported` |

# Sözleşmeler (P1 · contracts-v1 adayı)

Test yazarı testleri **yalnızca** `docs/requirements.md` ve bu dizinden yazar. Bir ad (uç nokta, alan, olay, hata kodu, Go imzası, sentinel, ortam değişkeni, metrik, senaryo, tablo/constraint) burada tanımlı değilse test onu kullanmaz; soru sahibe döner. Aynı ad her dosyada aynı yazılır; çelişki varsa öncelik: `openapi.yaml` (HTTP) > `sse-events.md` (SSE) > `go-interfaces.md` (Go) > diğerleri, ve çelişki bir hata olarak mimara bildirilir.

Dokümanın ötesine geçen kararlar kullanıldıkları yerde **VARSAYIM** olarak işaretlidir ve `DECISIONS.md`'ye taşınır. UI bileşenleri, props ve `data-testid`'ler [`ui.md`](ui.md)'dedir.

## 1. Dizin

| Dosya | İçerik |
|---|---|
| [`openapi.yaml`](openapi.yaml) | HTTP API (OpenAPI 3.1): 19 operasyon, şemalar, durum kodları, hata gövdesi |
| [`sse-events.md`](sse-events.md) | İçerik üretimi (`delta`, `invalid`, `validated`) ve canlı durum (`snapshot`, `status`, `completed`) olayları |
| [`schemas/segment-rule.schema.json`](schemas/segment-rule.schema.json) | Segment kuralı JSON şeması (LLM aracı girdisi + `POST /api/segments/count` gövdesi) |
| [`schemas/content.schema.json`](schemas/content.schema.json) | İçerik JSON şeması (LLM aracı girdisi + düzenleme gövdesi) |
| [`schemas/llm/segment-rule.tool.json`](schemas/llm/segment-rule.tool.json), [`schemas/llm/content.tool.json`](schemas/llm/content.tool.json) | Modele gönderilen strict-uyumlu araç şemaları (backend bunlarla doğrulamaz; llm-tools §1.1) |
| [`llm-tools.md`](llm-tools.md) | Araç tanımları, tek düzeltme döngüsü, sahte LLM senaryoları ve birebir fixture'lar |
| [`segment-dsl.md`](segment-dsl.md) | İzinli alanlar/operatörler, sınırlar, göreli zaman, parametreli SQL, izin filtresi, örnekler |
| [`go-interfaces.md`](go-interfaces.md) | Paket düzeni, Go imzaları, sentinel hatalar, test dikişleri |
| [`db-schema.md`](db-schema.md) | Tablolar, adlandırılmış constraint'ler, roller, migration kuralları, kuyruk mekanikleri |
| [`seed.md`](seed.md) | `cmd/seed` CLI, determinizm, kenar grupları, `manifest.json` şeması |
| [`mock-provider.md`](mock-provider.md) | Mock provider HTTP API, idempotency, 429, ortam değişkenleri, senaryolu mod |
| [`metrics.md`](metrics.md) | Prometheus metrik adları, `/healthz`, `/readyz` |
| [`ui.md`](ui.md) | Rotalar, bileşenler (props/emits/`data-testid`), hata → i18n eşlemesi, SSE istemci davranışı, UI güvenlik iddiaları |

## 2. Çalışma topolojisi (compose)

| Servis | Binary | İç port | Host'a yayın | Not |
|---|---|---|---|---|
| `web` | Vue (nginx) | 80 | `3000` | `/api/*`'yi `api:8080`'e proxy'ler (aynı origin) |
| `api` | `cmd/api` | 8080 | `8080` | HTTP API + SSE + `/healthz` `/readyz` `/metrics` |
| `worker` | `cmd/worker` | 8081 | — | Kanal başına dispatcher; `/healthz` `/readyz` `/metrics` |
| `db` | PostgreSQL 16 | 5432 | — | Yayınlanmaz (REQ-DLV-01) |
| `migrate` | `cmd/migrate` (karar §9.2) | — | — | Tek seferlik, `scc_migrator` |
| `seed` | `cmd/seed` | — | — | Tek seferlik, `migrate`'ten sonra |
| `mockprovider` | `cmd/mockprovider` | 8090 | — | Yayınlanmaz (REQ-DLV-01) |

`docker compose up` key'siz çalışır (`LLM_PROVIDER=auto` + boş key → mock). Host portları VARSAYIM.

## 3. Ortak konvansiyonlar

- Kimlikler: UUID string (küçük harf, tireli). Seed müşterileri deterministik UUIDv5, diğerleri UUIDv4.
- Zaman: RFC 3339, UTC, `Z` sonekli (`2026-01-15T12:00:00Z`); kesirli saniye olabilir.
- JSON: snake_case; `null` alanlar yanıtta **bulunur** (atlanmaz), aksi şemada belirtilmedikçe.
- API dili İngilizce (yollar, alanlar, kodlar, `message`); UI metni Türkçe ve koddan çevrilir.
- Metin uzunluğu: NFC normalize edilmiş metnin Unicode kod noktası (rune) sayısı.
- İstek gövdesi: `application/json`, en çok 65536 bayt (65537 → 413). Bilinmeyen alan → 422 `schema_violation`.
- Her yanıtta `X-Request-ID`. Kimlik doğrulama yok (VARSAYIM).
- Sabit test referans zamanı: `2026-01-15T12:00:00Z`; seed `--seed=42 --users=5000`.

## 4. Durum adları

- Kampanya: `draft`, `audience_ready`, `content_ready`, `approved`, `sending`, `completed` (kampanya düzeyinde `failed` yok). Geçişler: [`go-interfaces.md` §campaign `Transition`](go-interfaces.md#internalcampaign), DB tetikleyicisi [`db-schema.md` §3.4](db-schema.md#34-campaigns).
- Teslimat: `queued`, `sending`, `sent`, `failed`; `failure_reason` ∈ `provider_rejected`, `max_attempts_exceeded`, `rate_limit_budget_exhausted`, `content_too_long`, `recipient_unavailable`.
- İçerik sürümü: `status` ∈ `validated`, `invalid`; `source` ∈ `llm`, `edit`.
- Kanal: `sms`, `email`.

## 5. Hata gövdesi ve kodlar

```json
{
  "error": {
    "code": "validation_failed",
    "message": "content validation failed",
    "fields": [
      { "path": "sms.body", "code": "sms_too_long", "message": "SMS body exceeds 160 characters", "limit": 160, "actual": 161 }
    ],
    "request_id": "7f0c2d8e-3b55-4c3a-9f7e-2a7f1c9d0e11"
  }
}
```

`fields` yalnız `validation_failed` ve `llm_invalid_output`'ta bulunur (boş olmayan dizi). `message` asla sır, SQL veya DB hata metni içermez.

### 5.1 Üst düzey `error.code` (kapalı liste, 15)

| Kod | HTTP | Ne zaman |
|---|---|---|
| `malformed_request` | 400 | Gövde geçerli JSON değil veya `Content-Type` JSON değil |
| `not_found` | 404 | Bilinmeyen yol |
| `method_not_allowed` | 405 | Bilinen yol, desteklenmeyen HTTP metodu (`Allow` başlığıyla) |
| `campaign_not_found` | 404 | Bilinmeyen kampanya (biçimsiz UUID dahil) |
| `content_version_not_found` | 404 | Sürüm yok veya başka kampanyaya ait |
| `customer_not_found` | 404 | Önizlemede verilen müşteri kuralla eşleşmiyor |
| `invalid_state` | 409 | Kampanya durumu işleme izin vermiyor (onaysız gönderim, `sending`/`completed`'da düzenleme/üretim, ikinci gönderim, …) |
| `content_not_validated` | 409 | `validated` olmayan sürümü onaylama/önizleme |
| `stale_content_version` | 409 | En son sürüm olmayanı onaylama; onaylanandan farklı sürümle gönderim |
| `generation_in_progress` | 409 | Aynı kampanyada segment/içerik üretimi sürerken üretim, içerik düzenleme veya onay isteği (kilit istemci koptuğunda veya en geç 120 sn sonra bırakılır) |
| `payload_too_large` | 413 | Gövde > 64 KiB |
| `validation_failed` | 422 | Brief, içerik, segment kuralı, sorgu parametresi (`invalid_value`, path `query.<ad>`), senaryo başlığı veya onay-anı en kötü durum doğrulaması |
| `llm_invalid_output` | 502 | Düzeltme denemesinden sonra da geçersiz LLM çıktısı (segment) |
| `llm_unavailable` | 502 | LLM'e erişilemedi (segment; içerikte stream açılmadan önce) |
| `internal_error` | 500 | Beklenmeyen hata |

### 5.2 Alan hatası `fields[].code` (kapalı liste, 23)

| Kod | Kaynak | Go sentinel (`errors.Is`) |
|---|---|---|
| `required` | Alan **var ama boş** (brief, içerik alanları). Eksik anahtar `schema_violation`'dır | `validation.ErrRequired` |
| `too_long` | brief > 2000 | `validation.ErrTooLong` |
| `invalid_value` | segment değeri, sorgu parametresi, senaryo başlığı, `content_version_id` biçimi | `validation.ErrInvalidValue` |
| `invalid_json` | LLM çıktısı | `validation.ErrInvalidJSON` |
| `schema_violation` | LLM çıktısı, istek gövdesi (bilinmeyen/yanlış tipli alan), araç çağrılmadı | `validation.ErrSchemaViolation` |
| `truncated_output` | LLM çıktısı yarıda kesildi | (`llm.ErrTruncated` kaynaklı) |
| `no_tool_call` | Model yanıtında `tool_use` bloğu yok (path `""`) | (`llm.ErrNoToolCall` kaynaklı) |
| `llm_unavailable` | Yalnız içerik akışının son `invalid` olayında | (`llm.ErrUnavailable` kaynaklı) |
| `internal_error` | Yalnız içerik akışının son `invalid` olayında | — |
| `sms_too_long` | `sms.body` değişkenler en uzun değerlerle doldurulduktan sonra > 160 | `content.ErrSMSTooLong` |
| `subject_too_long` | `email.subject` doldurulduktan sonra > 80 | `content.ErrSubjectTooLong` |
| `email_body_too_long` | `email.body` doldurulduktan sonra > 5000 (VARSAYIM) | `content.ErrEmailBodyTooLong` |
| `sms_too_long_worst_case` | Onay anındaki yeniden denetimde en kötü durum SMS > 160 | `content.ErrSMSTooLong` + `content.ErrWorstCaseTooLong` |
| `subject_too_long_worst_case` | Onay anındaki yeniden denetimde en kötü durum konu > 80 | `content.ErrSubjectTooLong` + `content.ErrWorstCaseTooLong` |
| `unknown_variable` | İzinli olmayan `{{x}}` | `content.ErrUnknownVariable` |
| `malformed_variable` | Bozuk `{{` söz dizimi | `content.ErrMalformedVariable` |
| `unknown_field` | Segment alanı listede yok | `segment.ErrUnknownField` |
| `unknown_operator` | Segment operatörü listede yok | `segment.ErrUnknownOperator` |
| `operator_not_allowed` | Operatör alan tipine uymuyor | `segment.ErrOperatorNotAllowed` |
| `type_mismatch` | Değerin JSON tipi yanlış | `segment.ErrTypeMismatch` |
| `empty_group` | Boş `all`/`any` | `segment.ErrEmptyGroup` |
| `max_depth_exceeded` | Derinlik > 3 | `segment.ErrMaxDepthExceeded` |
| `max_conditions_exceeded` | Koşul > 10 | `segment.ErrMaxConditionsExceeded` |

### 5.3 Go → HTTP eşlemesi

| Go hatası | HTTP / kod |
|---|---|
| `*validation.Error` | 422 `validation_failed` (fields = `Fields`) |
| `campaign.ErrCampaignNotFound` / `ErrContentVersionNotFound` / `ErrCustomerNotFound` | 404 ilgili kod |
| `campaign.ErrInvalidState` / `ErrContentNotValidated` / `ErrStaleContentVersion` / `ErrGenerationInProgress` | 409 ilgili kod |
| `composer.ErrInvalidOutput` | 502 `llm_invalid_output` (fields = son denemenin hataları) |
| `llm.ErrUnavailable` | 502 `llm_unavailable` |
| (router) bilinmeyen yol / desteklenmeyen metot | 404 `not_found` / 405 `method_not_allowed` |
| diğer | 500 `internal_error` |

## 6. Ortam değişkenleri (tek liste)

| Değişken | Kullanan | Varsayılan | Tanım yeri |
|---|---|---|---|
| `ANTHROPIC_API_KEY` | api | boş | go-interfaces §config |
| `LLM_PROVIDER` | api | `auto` (`auto`\|`real`\|`mock`) | go-interfaces §config, §llm |
| `ANTHROPIC_MODEL`, `LLM_TIMEOUT`, `LLM_MAX_TOKENS` | api | `claude-opus-5-5`, `60s`, `4096` | llm-tools §1 |
| `MOCK_LLM_SCENARIO`, `MOCK_LLM_CHUNK_DELAY` | api | `ok`, `0s` | llm-tools §3 |
| `SCC_FORBID_REAL_LLM` | api (test) | boş; `compose.test.yml`'de `1` | go-interfaces §llm |
| `SEGMENT_REFERENCE_TIME` | api | boş (= saat) | segment-dsl §4 |
| `DATABASE_URL` | api, worker, seed | — | db-schema §1 |
| `MIGRATE_DATABASE_URL` | migrate | — | db-schema §1 |
| `HTTP_ADDR`, `WORKER_HTTP_ADDR` | api, worker | `:8080`, `:8081` | go-interfaces §config |
| `LOG_LEVEL` | hepsi | `info` | go-interfaces §config |
| `PROVIDER_BASE_URL`, `PROVIDER_TIMEOUT` | worker | `http://mockprovider:8090`, `10s` | go-interfaces §config |
| `DISPATCH_RATE_PER_SECOND`, `DISPATCH_CONCURRENCY`, `DISPATCH_BATCH_SIZE`, `DELIVERY_LEASE` | worker | `100`, `32`, `50`, `60s` | go-interfaces §config |
| `RETRY_MAX_ATTEMPTS`, `RETRY_BASE_DELAY`, `RETRY_MAX_DELAY`, `RETRY_MAX_RATE_LIMITED`, `RETRY_RATE_LIMIT_WINDOW` | worker | `5`, `1s`, `60s`, `20`, `10m` | go-interfaces §delivery |
| `WORKER_RAND_SEED` | worker | `0` (rastgele tohum) | go-interfaces §config |
| `MOCK_PROVIDER_*` (10 adet) | mockprovider | mock-provider §3 | mock-provider §3 |

## 7. Test ortamı sözleşmesi

`make test` = `docker compose -f compose.test.yml run --rm --build test` (PLAN §4.3). `test` konteynerinde:

| Değişken | Değer |
|---|---|
| `ANTHROPIC_API_KEY` | tanımsız |
| `LLM_PROVIDER` | `mock` |
| `SCC_FORBID_REAL_LLM` | `1` |
| `TEST_DATABASE_URL` | `scc_app` rolüyle test DB'si |
| `TEST_MIGRATE_DATABASE_URL` | `scc_migrator` rolüyle aynı DB |
| `E2E_API_URL` | `http://api:8080` (api: `LLM_PROVIDER=mock`, `SEGMENT_REFERENCE_TIME=2026-01-15T12:00:00Z`, seed `--seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z`) |
| `E2E_MOCKPROVIDER_URL` | `http://mockprovider:8090` (`MOCK_PROVIDER_ADMIN=true`) |

Test türleri ve konumları `.claude/skills/writing-tests/SKILL.md` §4'tedir. Gerçek Claude istemcisi test binary'sinde kurulamaz: `llm.New` → `llm.ErrRealClientForbidden`; `test/testenv.Main` bunu görünce `os.Exit(1)` (go-interfaces §0).

**Seed manifesti istisnası:** kodu yazan rol **tek bir** test yolu dosyası yazabilir: `testdata/seed/manifest.json` (seed kodunun ürettiği çıktı, `cmd/seed --skip-db`). Testler onu okur; bir determinizm testi `seed.Generate` + `MarshalCanonical` ile yeniden üretip baytları karşılaştırır. Test yazarı bu dosyayı **yazamaz**.

### 7.1 Hız sınırı testleri (REQ-SND-02)

- **Katı** `≤ 100` iddiası yalnızca sahte saatli unit testlerde, **istek başlangıç** zamanları üzerinde yapılır: `delivery.NewTokenBucket(clock.Fake, 10ms)` ve `Dispatcher.ProcessOnce` ile sahte `provider.Client`'ın kaydettiği `Clock.Now()` değerleri; her yarı açık `[t, t+1s)` penceresinde ≤ 100. Birden çok goroutine veya birden çok `Dispatcher` kullanan unit testler **tek bir `RateLimiter` örneğini paylaşır** (`ProcessOnce` kanal kilidini almaz; her `NewTokenBucket` kendi başına 100/sn verir).
- **"Worker sayısından bağımsız"** özelliği entegrasyon testinde gerçek DB ile sınanır: aynı kanal için iki `Dispatcher.Run` (her biri **kendi** `TokenBucket`'ı ve ayrı kayıt tutan sahte `provider.Client`'ıyla) çalıştırılır; yalnız advisory lock'u tutan (`Dispatcher.Leader() == true`, `Recorder.SetLeader`/`scc_dispatcher_leader{channel}` = 1) dispatcher provider'ı çağırır, diğerinin sahte provider'ı hiç istek almaz; lider durdurulunca diğeri kilidi alıp devam eder.
- E2E / gerçek mock provider testlerinde varış zamanları ağ titremesi içerir; burada **toleranslı** iddia kullanılır: `/admin/requests` `received_at` değerleri üzerinde her `[t, t+1s)` penceresinde ≤ **110**, ve tüm koşu için (istek sayısı − 1) / (son − ilk varış, sn) ≤ **101**. Katı ≤ 100 E2E'de iddia edilmez.

## 8. REQ → sözleşme eşlemesi (38/38)

| REQ | Gözlemlenebilir yüzeyin tanımlandığı yer |
|---|---|
| REQ-AUD-00 | openapi `POST /api/campaigns`, `POST /api/campaigns/{id}/segment` (`SegmentResult`); llm-tools §3 `ok` → `SEG_OK`; seed §5 `example_brief.rule/audience`; segment-dsl §7.1 |
| REQ-AUD-01 | segment-dsl §1.1, §3, §7.3; go-interfaces §segment (`Parse`, `Validate`, sentinel'ler); openapi `POST /api/segments/count` (422) ve `/segment` (502); llm-tools §2 |
| REQ-AUD-02 | segment-dsl §5 (`Compiled.Where`/`Args`), §7.3 #1–3; llm-tools `sql_in_value`, `schema_violation`, `injection_attempt`; openapi `BadGateway` (DB metni yok) |
| REQ-AUD-03 | openapi `Audience` (`matched`, `sms`, `email`); seed §5 `example_brief.audience`; segment-dsl §6; **ui.md §3.7** (`audience-count-*`) |
| REQ-AUD-04 | segment-dsl §6; openapi `sendCampaign`, `listDeliveries` (`customer_id`); seed §3.3 gruplar; go-interfaces `Dispatcher.ProcessOnce` adım 1 (`recipient_unavailable`) |
| REQ-AUD-05 | openapi `createCampaign` (422 `required`/`too_long`, 413); go-interfaces `campaign.NormalizeBrief`; ui.md §3.5 |
| REQ-CNT-01 | go-interfaces `content.Validate(c, Longest)` (doldurma **sonrası** uzunluk), `SMSMaxRunes`, `ErrSMSTooLong`, `RuneCount`; llm-tools §2; ui.md §3.13 |
| REQ-CNT-02 | go-interfaces `content.Validate` (`SubjectMaxRunes`, `ErrSubjectTooLong`); ui.md §3.13 |
| REQ-CNT-03 | go-interfaces `content.Variables`, `Render`, `EffectiveFirstName`, `FirstNameFallback`, `ErrUnknownVariable`, `ErrMalformedVariable`; openapi `previewContent`; seed `empty_first_name`; ui.md §3.12, §3.15 |
| REQ-CNT-04 | sse-events §1; openapi `generateContent`; llm-tools `then_ok_after_repair`, `truncated_stream`, `stalled_stream`; openapi `approveContent` (409 `content_not_validated`); **ui.md §3.10, §3.17, §5.2** |
| REQ-CNT-05 | go-interfaces `content.Validate`, `MeasureWorstCase`, `CheckWorstCase`, `CheckRendered`, `Longest`; openapi `Segment.longest_values`, `ContentVersion.worst_case`, `approveContent` adım 6; db-schema `failure_reason = content_too_long`; seed `example_brief.longest_values`, `long_first_name`; ui.md §3.13 |
| REQ-APR-01 | openapi `sendCampaign` (409, teslimat yok), `editContent`, `previewContent`, `approveContent`; **ui.md §3.14–3.18** |
| REQ-APR-02 | openapi `editContent` (aynı `content.Parse`+`Validate`, 422); llm-tools §2; ui.md §3.14 |
| REQ-APR-03 | openapi `approveContent` (`stale_content_version`), `editContent`, `generateSegment`; db-schema §3.6; go-interfaces `campaign.Transition`; **ui.md §3.17 `stale`** |
| REQ-SND-01 | openapi `sendCampaign` (202, tek işlem, `SendResult`); db-schema §3.7; openapi `listDeliveries` |
| REQ-SND-02 | go-interfaces `delivery.NewTokenBucket`/`RateLimiter` (paylaşılan örnek), `Dispatcher.Leader`, `Recorder`, `LockKeySMS/LockKeyEmail`; metrics `scc_dispatcher_leader`; db-schema §4; **README §7.1**: katı ≤ 100 yalnız sahte saatli unit testte istek **başlangıçları** üzerinde; worker sayısından bağımsızlık iki `Dispatcher.Run` ile entegrasyon testinde; E2E'de `/admin/requests` varışları üzerinde toleranslı (≤ 110 / pencere, ortalama ≤ 101) |
| REQ-SND-03 | db-schema `deliveries_campaign_channel_customer_key` (SQLSTATE 23505), §4 claim (`SKIP LOCKED`), lease; mock-provider §1.1 idempotency + "yanıt kayboldu", `/admin/messages.request_count`; seed `shared_contact` |
| REQ-SND-04 | go-interfaces `delivery.RetryPolicy.Next`/`Jitter`, `RetryConfig`, `Decision`, `CountKind`; mock-provider §4 senaryolu mod; openapi `Delivery` sayaçları, `failure_reason`, `last_error` |
| REQ-SND-05 | mock-provider giriş + §1.1 (`invalid_recipient`); README §2 (`PROVIDER_BASE_URL`); seed §3.1; db-schema kurgusal CHECK'ler |
| REQ-TRK-01 | sse-events §2; openapi `getCampaignStatus`, `streamCampaignEvents`; go-interfaces §httpapi (1 sn yoklama); db-schema §4 tamamlama; **ui.md §3.20, §3.21, §5.3, §5.4** |
| REQ-TRK-02 | metrics §1 (`scc_deliveries_sent_total`, `scc_deliveries_failed_total`, `scc_delivery_retries_total`, `scc_deliveries_queued`) |
| REQ-TRK-03 | metrics §3; openapi `getHealthz`, `getReadyz`; go-interfaces §health |
| REQ-AI-01 | schemas/*.json (tam) + schemas/llm/*.tool.json (strict); llm-tools §1–3; sse-events §1; openapi `generateSegment` (502), `generateContent` |
| REQ-AI-02 | go-interfaces §llm (`Mock`, `Scenario*`, `WithScenario`, `ErrRealClientForbidden`); llm-tools §3; README §7 |
| REQ-AI-03 | go-interfaces `llm.Select`, `llm.New` (log kaydı, hata önceliği), `ErrMissingAPIKey`; openapi `getConfig`; **ui.md §3.3** (`provider-badge`) |
| REQ-AI-04 | go-interfaces §secret, `config.Config.String/LogValue`, `llm.New` log alanları; openapi hata `message` kuralı |
| REQ-AI-05 | go-interfaces §config (`ANTHROPIC_API_KEY`); README §6. `.env.example`: çalışma zamanı yüzeyi yok (repo dosyası) |
| REQ-MOCK-01 | mock-provider §1, §3 |
| REQ-MOCK-02 | mock-provider §1.1 (replay), §2 (kendi 100/sn sınırı, `Retry-After`) |
| REQ-UI-01 | openapi `previewContent` (`email.html` kaçışlı); go-interfaces `content.RenderEmailHTML`; llm-tools `html_in_content`; **ui.md §6, §3.16** |
| REQ-SEC-01 | go-interfaces §delivery log kayıtları; mock-provider §5; ui.md §6.5 |
| REQ-SEC-02 | db-schema §1 (`scc_app`, SQLSTATE `42501`); README §7 |
| REQ-SEED-01 | seed §1–5; go-interfaces §seed |
| REQ-SEED-02 | seed §3.1; db-schema kurgusal CHECK'ler, olmayan kolonlar |
| REQ-DLV-01 | README §2; seed §1 compose sırası. Dosya: `compose.yml` (sözleşme dışı implementasyon) |
| REQ-DLV-02 | README §7; go-interfaces §0 (`testenv.Main` → `os.Exit(1)`), `llm.ErrRealClientForbidden` + `SCC_FORBID_REAL_LLM` |
| REQ-DLV-03 | Çalışma zamanı yüzeyi yok (süreç: `ai-sessions/`, `make sanitize-sessions`, `make check-sessions`) |
| REQ-DLV-04 | Çalışma zamanı yüzeyi yok (süreç: `README.md`, `DECISIONS.md`) |

## 9. Kararlar (sahip / lider, P1 incelemesi)

Hepsi `DECISIONS.md`'ye gerekçesiyle yazılır.

1. **`tool_choice: auto` + `strict: true`** (llm-tools §1.2): `claude-opus-5-5` / `claude-sonnet-5-5` zorunlu araç seçimini 400 ile reddeder. Araç çağrılmazsa ayrı kod `no_tool_call` + düzeltme denemesi. Segment ve içerik için tek mekanizma tool use; `output_config` değerlendirildi, seçilmedi.
2. **`cmd/migrate`** ayrı tek seferlik binary — kabul.
3. **`testdata/seed/manifest.json`**: kodu yazan rol tek bir test yolu dosyası yazabilir, o da bu dosyadır (seed kodunun çıktısı); testler okur, determinizm testi baytları yeniden üretir; test yazarı bu dosyayı yazamaz (§7).
4. **"Alışveriş yapmayan" = son 30 günde satın alma yok** — kabul; diğer yorumu ayırt eden seed grubu `purchase_before_cart_add`.
5. **En kötü durum kümesi** = kuralla eşleşen herkes (izinden önce) — kabul. Uzunluk kuralı LLM çıktısı ve düzenlemede doğrulama anında uygulanır; onayda yeniden denetlenir.
6. **429 bütçesi**: 20. 429 teslimatı `failed` yapar; `Retry-After` 10 dk penceresini aşacaksa hemen `failed` — kabul.
7. **DB'den türetilen counter'lar** seed `--reset` sonrası azalabilir — kabul.
8. **`POST /api/segments/count`** ve **409 `generation_in_progress`** — kabul; `stalled_stream` senaryosu 409'u talep üzerine üretir.
9. **Strict şema katmanı**: tam şemalar backend'de, strict varyantlar (`schemas/llm/*.tool.json`) yalnız modele; uzunluk/sayı/derinlik kuralları yalnız backend doğrulayıcısında.
10. **Başarısız yeniden üretim** onayı düşürmez; `invalid` taslaklar "en son sürüm" olmaz (VARSAYIM, sse-events §1.2 #6).
11. **Canlı durum** sunucuda 1 sn'lik `Service.Status` yoklamasıyla beslenir; `LISTEN/NOTIFY` yok.
12. **Üretim kilidi**: üretim sürerken onay ve içerik düzenleme de 409 `generation_in_progress` döner (önceki `validated` sürüm de onaylanamaz). Kilit istek `ctx`'i iptal edilince ve en geç `campaign.MaxGenerationDuration` = 120 sn sonra bırakılır (VARSAYIM).
13. **HTTP 200 sonrası `llm_unavailable`**: düzeltme denemesi yok, son `invalid`, durum değişmez.
14. Sahte LLM'in adı her yerde **`llm.Mock`**.

## 10. Açık noktalar

1. ~~Strict birleşim sınırının sayım biçimi~~ **Kapandı (2026-10-26):** gerçek API çağrısıyla segment ve içerik aracı `strict: true` kabul edildi (llm-tools §1.1). Geri dönüş (`strict: false`) yalnız ileride 400 gelirse devreye girer; `DECISIONS.md`'ye D23 olarak yazıldı.

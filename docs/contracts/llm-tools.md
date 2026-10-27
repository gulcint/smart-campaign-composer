# LLM araçları, düzeltme döngüsü ve sahte LLM senaryoları

Kapsam: REQ-AUD-00, REQ-AUD-01, REQ-AI-01, REQ-AI-02, REQ-AI-03, REQ-CNT-04, REQ-UI-01 (fixture).
Şemalar: tam doğrulama şemaları [`schemas/segment-rule.schema.json`](schemas/segment-rule.schema.json), [`schemas/content.schema.json`](schemas/content.schema.json); modele gönderilen strict varyantlar [`schemas/llm/segment-rule.tool.json`](schemas/llm/segment-rule.tool.json), [`schemas/llm/content.tool.json`](schemas/llm/content.tool.json). Go imzaları: [`go-interfaces.md` §llm, §composer](go-interfaces.md#internalllm).

## 1. Araç (tool) tanımları

Karar: segment **ve** içerik için tek mekanizma **tool use**'tur (tek doğrulama, tek düzeltme yolu). `output_config` ile yapılandırılmış çıktı değerlendirildi, seçilmedi: alan bazında akış parçası vermiyor ve `tool_choice` ile etkileşimi belgelenmemiş.

| Ad (`llm.ToolEmit…`) | Kullanım | Gönderilen `input_schema` (`llm.ToolSchema(name)`) | Açıklama (modele giden `description`) |
|---|---|---|---|
| `emit_segment_rule` | Segment üretimi (stream yok) | `schemas/llm/segment-rule.tool.json` | "Return the target audience for the marketing brief as a structured segment rule. Use only the listed fields and operators. Never output SQL. Channel consent is applied by the system; do not try to express it. Use value null for is_null / is_not_null." |
| `emit_campaign_content` | İçerik üretimi (stream) | `schemas/llm/content.tool.json` | "Return the SMS and email copy for the campaign in Turkish. SMS body at most 160 characters and email subject at most 80 characters AFTER personalization; the longest recipient values are given in the prompt. Allowed variables: {{first_name}}, {{last_name}}, {{city}}. Plain text only, no HTML." |

### 1.1 İki şema katmanı

| Katman | Dosya | Kim kullanır | İçerik |
|---|---|---|---|
| **Tam şema** | `schemas/*.schema.json` | Backend (`segment.Parse`, `content.Parse`) ve testler | Yapı + `minLength`, `maxItems`, `oneOf`, isteğe bağlı `value` |
| **Strict varyant** | `schemas/llm/*.tool.json` | Yalnız modele `input_schema` olarak gönderilir (`strict: true`) | Aynı yapı; strict modda desteklenmeyen anahtarlar çıkarılmış |

- Strict varyantta **bulunmayacak** anahtar sözcükler (unit test şartı, `llm.ToolSchema` çıktısı üzerinde): `minLength`, `maxLength`, `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`, `minItems`, `maxItems`, `pattern`, `format`, `const`, `oneOf`; dış `$ref` (yalnız `#/$defs/...`); özyineleme (bir `$defs` girdisi kendine/üst seviyesine geri başvurmaz); `false` dışında `additionalProperties`. Her nesnede `additionalProperties: false` ve tüm özellikler `required`.
- Varyant farkları: `oneOf` → `anyOf`; dizi sınırları yok; segmentte koşulun `value` alanı zorunludur (`is_null`/`is_not_null` için `null`).
- **Unit test şartı:** §3.3'teki her geçerli-yapılı fixture (`SEG_OK`, `SEG_SQL_VALUE`, `SEG_UNKNOWN_FIELD`, `CNT_OK`, `CNT_SMS_161`, `CNT_SUBJECT_81`, `CNT_UNKNOWN_VAR`, `CNT_HTML`) **tam** şemaya göre geçerlidir; uzunluk/sayı/derinlik kuralları yalnız backend doğrulayıcısındadır.
- Strict sınırları (belgelenen: istek başına en çok 20 strict araç, 24 isteğe bağlı parametre, 16 birleşim tipli parametre). Sayım (tanım başına, `$defs` açılmadan): segment aracı **0** isteğe bağlı, **10** `anyOf` birleşimi (`value` 1; `group1`, `group2`, `group3` 3; `group1`/`group2` çocukları 6); içerik aracı 0 / 0; istek başına 1 strict araç. **Doğrulandı (2026-10-26, gerçek API, `claude-sonnet-5-5`):** hem segment hem içerik aracı `strict: true` ile HTTP 200 aldı ve şemaya uygun `tool_use` döndü; her ikisi strict gider. Gelecekte API 400 (şema karmaşıklığı) dönerse geri dönüş: o aracı `strict: false` gönder, backend doğrulaması değişmez (README §9).
- Eski "şema dosyasının birebir kopyası" ifadesi geçersizdir.

### 1.2 Çağrı biçimi (gerçek Claude istemcisi; testlerde çalışmaz)

- SDK: `github.com/anthropics/anthropic-sdk-go/v2` (Go ≥ 1.25). `tool_choice` alanının Go'daki adı belgelenmemiştir; implementasyonda pkg.go.dev üzerinden doğrulanacaktır.
- Model: `ANTHROPIC_MODEL` (varsayılan `claude-opus-5-5`), `max_tokens` = `LLM_MAX_TOKENS` (varsayılan 4096), zaman aşımı `LLM_TIMEOUT` (varsayılan `60s`). `temperature`, `top_p`, `top_k` **gönderilmez** (`claude-opus-5-5`/`claude-sonnet-5-5` bunları reddeder).
- `tool_choice: {"type":"auto"}` + `strict: true` + sistem talimatı ("Always answer by calling the `<tool>` tool exactly once"). Gerekçe: `claude-opus-5-5` ve `claude-sonnet-5-5` zorunlu `tool_choice` (`tool`/`any`) ile 400 döner. Not: `claude-haiku-5-5` zorunlu `tool_choice`'u kabul eder (thinking bloğu olmadan); `ANTHROPIC_MODEL` Haiku'ya ayarlanırsa istemci `{"type":"tool","name":…}` gönderebilir — davranış sözleşmesi değişmez.
- Yanıtta `tool_use` bloğu yoksa (metin veya başka araç) istemci `llm.ErrNoToolCall` döner → alan hatası `{"path":"","code":"no_tool_call"}`; geçersiz çıktı gibi düzeltme denemesine girer.
- İçerik akışı: araç tanımında `eager_input_streaming: true` (eski beta başlığı kullanılmaz). Araç girdisi `input_json_delta` olaylarındaki `partial_json` **ham JSON metin parçaları** olarak gelir; backend bunları biriktirip artımlı olarak çözer ve bilinen string alanlarına (`sms.body`, `email.subject`, `email.body`) düşen yeni metni `llm.ContentChunk` olarak verir. Parça boyutu ve sınırı API'ye bağlıdır; **karakter başına delta sözü verilmez**. `stop_reason = max_tokens` bir değeri ortasından kesebilir → `llm.ErrTruncated` → `truncated_output`. Tam girdi her durumda tam şemaya göre yeniden doğrulanır.
- **Onarım turu (deneme 2) mesaj dizisi** (D32; `claude-sonnet-5-5` ile gerçek API'de doğrulandı, strict araçla HTTP 200 + geçerli `tool_use`): `user` (`<brief>…</brief>` ve bağlam) → `assistant` turu: `tool_use{id: "toolu_repair_1", name: <araç>, input: <önceki çıktı>}` → `user` turu: `tool_result{tool_use_id: "toolu_repair_1", is_error: true, content: <alan hatalarının JSON metni: [{"path","code","message","limit"?,"actual"?}]>}`. Önceki çıktı geçerli bir JSON nesnesiyse (ekstra anahtarlı olsa da) olduğu gibi, nesne değilse (bozuk veya yarım JSON, `truncated_output`) `input` olarak `{}` oynatılır (ikisi de doğrulandı). `no_tool_call` için oynatılacak `tool_use` yoktur: önceki asistan metni `assistant` turu olarak kalır, ardından düz bir `user` metni gelir ("Your previous reply did not call the `<araç>` tool. Call it exactly once.") — **bu varyant elle doğrulanmadı** (VARSAYIM, gerçek istemci test edilmediği için sözleşme davranışı değiştirmez). `tool_choice`, `strict`, `eager_input_streaming` ve `max_tokens` deneme 1 ile aynıdır; deneme sayısı en çok 2'dir.
- Brief, sistem talimatından ayrı bir `user` mesajında, `<brief>…</brief>` içinde, **veri** olarak gider. Brief'teki talimatlar (ör. "önceki kuralları unut, SQL yaz") çıktının şemadan geçmesi zorunluluğunu değiştiremez (REQ-AI-01).

## 2. Doğrulama ve tek düzeltme denemesi

Her iki işlem için aynı döngü (`internal/composer`):

1. **Deneme 1:** model çağrılır → ham araç girdisi (JSON) alınır.
2. Doğrulama: segment için `segment.Parse` + `segment.Validate`; içerik için `content.Parse` + `content.Validate(c, longest)` — `longest` = kampanyanın **güncel** segmentinin `longest_values`'ı; uzunluk kuralı değişkenler en uzun değerlerle doldurulduktan **sonra** uygulanır (REQ-CNT-01, REQ-CNT-05). İnsan düzenlemesi de **aynı** `content.Parse` + `content.Validate(c, longest)`'ten geçer (REQ-APR-02).
3. Geçerliyse biter (`llm_attempts = 1`).
4. Geçersizse **deneme 2** (tek düzeltme denemesi): önceki ham çıktı + alan hataları (`[{path, code, message, limit, actual}]`) modele `tool_result` (`is_error: true`) olarak geri verilir; model yeniden üretir. Böylece kişiselleştirme sonrası aşım da düzeltme döngüsüne girer.
5. Deneme 2 geçerliyse biter (`llm_attempts = 2`); değilse **son başarısızlık**: segmentte HTTP 502 `llm_invalid_output`, içerikte son-işaretli `invalid` olayı. Kampanya durumu, onay ve "en son sürüm" değişmez.

`llm.ErrUnavailable` (ağ/5xx/zaman aşımı/SDK hatası) düzeltme denemesini **tetiklemez**: segmentte doğrudan 502 `llm_unavailable`; içerikte stream açılmadan önceyse HTTP 502 `llm_unavailable` (JSON); HTTP 200 sonrası **herhangi bir anda** (deneme 1'in ortası dahil) olursa düzeltme denemesi yapılmaz, akış son `invalid` olayıyla biter (`final: true`, `errors: [{"path":"","code":"llm_unavailable"}]`, `content_version_id: null`), kampanya durumu değişmez.

**`no_tool_call` her iki işlemde de tanımlıdır:** segmentte `GenerateSegment` → `llm.ErrNoToolCall`; içerikte yanıt `tool_use` bloğu olmadan biterse `ContentStream.Recv` → `llm.ErrNoToolCall`. Her ikisi `{"path":"","code":"no_tool_call"}` geçersiz çıktısıdır ve tek düzeltme denemesini tetikler (içerikte deneme 1 ise `invalid` `final:false`).

**Üretim kilidi:** kampanya başına aynı anda tek üretim (segment veya içerik); kilit istek `ctx`'i iptal edildiğinde (istemci koptu) veya en geç `campaign.MaxGenerationDuration` = **120 sn** (VARSAYIM) sonra bırakılır; süre dolarken çalışan içerik akışı son `invalid` (`llm_unavailable`) ile biter. Kilit tutulurken segment/içerik üretimi, içerik düzenleme, onay ve gönderim **409 `generation_in_progress`** döner.

LLM çıktısından gelen hata kodları (alan hatası `code`): `invalid_json`, `schema_violation`, `truncated_output`, `no_tool_call`, `llm_unavailable` + segment/içerik doğrulayıcısının kodları (bkz. [README §5](README.md#5-hata-gövdesi-ve-kodlar)).

## 3. Sahte LLM senaryoları

Sahte LLM (`llm.Mock`) brief içeriğine **bakmaz**; çıktıyı yalnızca seçili senaryo ve deneme numarası belirler. Tamamen deterministiktir (saat, rastgelelik, ağ yok).

### 3.1 Senaryo seçimi

| Mekanizma | Kural |
|---|---|
| HTTP başlığı `X-Mock-LLM-Scenario: <ad>` | Segment üretimi (`POST /api/campaigns/{id}/segment`) ve içerik üretimi (`POST /api/campaigns/{id}/content`) isteklerinde okunur. **Yalnızca etkin sağlayıcı `mock` iken** dikkate alınır; `claude` iken sessizce yok sayılır. Bilinmeyen ad (mock modunda) → 422 `validation_failed`, alan `path: "header.X-Mock-LLM-Scenario"`, `code: "invalid_value"`, LLM çağrılmaz. |
| Ortam değişkeni `MOCK_LLM_SCENARIO` | Başlık yoksa kullanılan varsayılan. Varsayılanı `ok`. |
| Go | `llm.WithScenario(ctx, llm.ScenarioX)` başlığın karşılığıdır; `llm.MockOptions.DefaultScenario` ortam değişkeninin karşılığıdır. |

Senaryo adları büyük/küçük harf duyarlıdır. `GET /api/config` mock modunda `mock_scenarios` listesini döner.

### 3.2 Senaryo tablosu

"Her deneme" = deneme 1 ve deneme 2 aynı çıktıyı üretir → son başarısızlık.

| Senaryo | Segment (`GenerateSegment`) | İçerik (`StreamContent`) | Beklenen son sonuç (segment / içerik) |
|---|---|---|---|
| `ok` | `SEG_OK` | `CNT_OK` | 200, `llm_attempts=1` / `validated` (attempt 1) |
| `invalid_json` | her deneme `SEG_BAD_JSON` | her deneme `CNT_BAD_JSON` | 502 `llm_invalid_output` (`invalid_json`) / son `invalid`, `content_version_id: null` |
| `schema_violation` | her deneme `SEG_SQL_PROP` | her deneme `CNT_EXTRA_PROP` | 502 (`schema_violation`, path `sql`) / son `invalid` (`schema_violation`, path `html`), `content_version_id: null` |
| `over_limit_sms` | `SEG_OK` | her deneme `CNT_SMS_161` | 200 / son `invalid` (`sms_too_long`, `sms.body`, `actual` 161), **`content_version_id` dolu** (status `invalid`; en son sürüm olmaz, durum değişmez) |
| `over_limit_subject` | `SEG_OK` | her deneme `CNT_SUBJECT_81` | 200 / son `invalid` (`subject_too_long`, `email.subject`), `content_version_id` dolu |
| `unknown_variable` | `SEG_OK` | her deneme `CNT_UNKNOWN_VAR` | 200 / son `invalid` (`unknown_variable`, `sms.body`), `content_version_id` dolu |
| `injection_attempt` | her deneme `SEG_INJECTION` | her deneme `CNT_INJECTION` | 502 (`schema_violation`, path `rule.raw_sql`) / son `invalid` (`schema_violation`, path `sql`), `content_version_id: null` |
| `truncated_stream` | her deneme `llm.ErrTruncated` | her deneme `CNT_TRUNCATED` | 502 (`truncated_output`) / son `invalid` (`truncated_output`, path `""`), `content_version_id: null` |
| `then_ok_after_repair` | deneme 1 `SEG_UNKNOWN_FIELD`, deneme 2 `SEG_OK` | deneme 1 `CNT_SMS_161`, deneme 2 `CNT_OK` | 200, `llm_attempts=2` / `invalid`(final=false) → `validated` (attempt 2) |
| `always_invalid` | deneme 1 `SEG_BAD_JSON`, deneme 2 `SEG_SQL_PROP` | deneme 1 `CNT_BAD_JSON`, deneme 2 `CNT_EXTRA_PROP` | 502 (`schema_violation`) / son `invalid` (`schema_violation`), `content_version_id: null` |
| `sql_in_value` | `SEG_SQL_VALUE` (geçerli kural) | `CNT_OK` | 200, `audience.matched = 0`, tablolar yerinde / `validated` |
| `html_in_content` | `SEG_OK` | `CNT_HTML` (geçerli içerik) | 200 / `validated`; önizlemede HTML kaçışlı (REQ-UI-01) |
| `llm_unavailable` | `llm.ErrUnavailable` | `StreamContent` başlarken `llm.ErrUnavailable` | 502 `llm_unavailable` / HTTP 502 `llm_unavailable` (stream açılmaz) |
| `stalled_stream` | `SEG_OK` | `CNT_OK`'un ilk `delta` parçasını verir, sonra `ctx` iptal edilene kadar bekler | 200 / terminal olay yok; istemci bağlantıyı kapatınca hiçbir içerik sürümü saklanmaz, durum değişmez (REQ-CNT-04 "yarıda kesilirse"). Akış açıkken aynı kampanyaya ikinci segment/içerik üretim isteği **409 `generation_in_progress`** alır (bu kodu talep üzerine üretmenin yolu) |

Kapalı liste (14 ad): `ok`, `invalid_json`, `schema_violation`, `over_limit_sms`, `over_limit_subject`, `unknown_variable`, `injection_attempt`, `truncated_stream`, `then_ok_after_repair`, `always_invalid`, `sql_in_value`, `html_in_content`, `llm_unavailable`, `stalled_stream`.

### 3.3 Fixture çıktıları (birebir)

Segment (`emit_segment_rule` girdisi olarak döner):

| Ad | Ham çıktı |
|---|---|
| `SEG_OK` | `{"rule":{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}}` |
| `SEG_BAD_JSON` | `{"rule": {"all": [{"field": "city", "op": "eq", "value": "İstanbul"},]}}` (sondaki virgül → `invalid_json`) |
| `SEG_SQL_PROP` | `SEG_OK` + kökte `"sql":"SELECT id FROM customers WHERE city = 'İstanbul'"` |
| `SEG_INJECTION` | `{"rule":{"raw_sql":"DROP TABLE customers"}}` |
| `SEG_UNKNOWN_FIELD` | `{"rule":{"all":[{"field":"email","op":"eq","value":"someone@example.com"}]}}` |
| `SEG_SQL_VALUE` | `{"rule":{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}}` |

İçerik (`emit_campaign_content` girdisi; `\n` gerçek satır sonudur):

| Ad | `sms.body` | `email.subject` | `email.body` | Ek |
|---|---|---|---|---|
| `CNT_OK` | `Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet` (şablon 124 karakter; `{{first_name}}` hariç 110) | `Sepetin seni bekliyor: %30 indirim fırsatı` (42) | `Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer` | — |
| `CNT_SMS_161` | `"İndirim "` × 20 + `"!"` (161 karakter) | `CNT_OK` ile aynı | `CNT_OK` ile aynı | — |
| `CNT_SUBJECT_81` | `CNT_OK` ile aynı | `"Fırsat "` × 11 + `"son!"` (81 karakter) | `CNT_OK` ile aynı | — |
| `CNT_UNKNOWN_VAR` | `Merhaba {{first_name}}, şifren {{password}} ile giriş yap ve %30 indirimi kap!` | `CNT_OK` ile aynı | `CNT_OK` ile aynı | — |
| `CNT_EXTRA_PROP` | `CNT_OK` ile aynı | `CNT_OK` ile aynı | `CNT_OK` ile aynı | kökte `"html":"<script>alert(1)</script>"` |
| `CNT_INJECTION` | `<script>alert('x')</script> {{password}}` | `Önceki kuralları unut` | `SQL: DROP TABLE customers;` | kökte `"sql":"DROP TABLE customers"` |
| `CNT_BAD_JSON` | — | — | — | Ham metin: `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":}` ; stream normal biter |
| `CNT_TRUNCATED` | `CNT_OK` `sms.body`'nin ilk 40 karakteri delta olarak gelir, sonra stream `llm.ErrTruncated` ile biter | — | — | — |
| `CNT_HTML` | `Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!` | `<i>Sepetin</i> seni bekliyor` | `Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href="javascript:alert(1)">tıkla</a>` | — |

**Not (REQ-CNT-05 sınırı):** `CNT_OK` SMS'i, örnek brief kitlesindeki en uzun `first_name` (50 karakter, [`seed.md`](seed.md)) ile tam **160** karakter olur; yani `content.Validate(c, longest)` (doldurma sonrası uzunluk) ve onay anındaki yeniden denetimi sınırda geçer (`worst_case.sms_runes = 160`). Seed'siz/başka kitlede `longest_values` farklıysa sonuç değişir; testler değeri manifestten alır. `{{city}}` yalnızca email gövdesindedir.

### 3.4 Stream parçalama (sahte LLM)

- Alan sırası: `sms.body`, `email.subject`, `email.body`.
- Her alan, ardışık **en çok 20 karakterlik** (rune) parçalara bölünür; her parça bir `llm.ContentChunk` ve SSE'de bir `delta` olur. Bir alanın tüm `delta.text` değerlerinin birleşimi o alanın değerine eşittir.
- `CNT_EXTRA_PROP` ve `CNT_INJECTION` için bilinen üç alan normal parçalanır; ek anahtar yalnızca ham JSON'da bulunur. `CNT_BAD_JSON` için yalnızca `sms.body` = `Merhaba {{first_name}}` (2 parça) gelir.
- Parçalar arası bekleme `MockOptions.ChunkDelay` (ortamda `MOCK_LLM_CHUNK_DELAY`, varsayılan `0s`; compose demo `40ms`). Bekleme enjekte edilen `Clock` ile yapılır; testlerde 0.

### 3.5 İçerik akışı sonuçları hangi katmanda test edilir

E2E (docker, `X-Mock-LLM-Scenario`) yalnız yukarıdaki 14 senaryoyu üretebilir. Senaryosu olmayan sonuçlar için test, `campaign.Deps.LLM` veya `composer.OpenContent`'e **test içi bir `llm.Client` sahtesi** verir (`llm.Client` ve `llm.ContentStream` genel arayüzlerdir; ham çıktıyı ve hataları sahte belirler). Yeni senaryo adı eklenmez (kapalı liste 14).

| Davranış | Katman ve yöntem |
|---|---|
| `validated` (deneme 1 ve 2), `then_ok_after_repair`, `over_limit_sms`, `over_limit_subject`, `unknown_variable`, `schema_violation`, `invalid_json`, `truncated_stream`, `injection_attempt`, `always_invalid`, `html_in_content` | E2E (HTTP + SSE + DB) veya HTTP entegrasyon; senaryo başlığı |
| Stream açılmadan `llm_unavailable` → HTTP 502 JSON, SSE yok, durum değişmez | E2E, senaryo `llm_unavailable` |
| Aynı kampanyada ikinci üretim/düzenleme/onay/gönderim → 409 `generation_in_progress`; istemci kopunca kilit bırakılır ve sürüm saklanmaz | E2E, senaryo `stalled_stream` + bağlantıyı kapatma |
| HTTP 200 **sonrası** erişim kaybı → son `invalid` (`final: true`, `llm_unavailable`, `content_version_id: null`), düzeltme yok, durum aynı | `composer.ContentSession.Run` unit ve Service entegrasyonu; sahte `ContentStream` bir `ContentChunk` verir, sonra `Recv` → `llm.ErrUnavailable` |
| 120 sn (`campaign.MaxGenerationDuration`) aşımı → son `invalid` `llm_unavailable`, kilit bırakılır | Service entegrasyonu; `clock.Fake` + bloklayan sahte `ContentStream`; saat 120 sn ilerletilir |
| LLM çıktısı olarak boş değer (`required`), `malformed_variable`, `email_body_too_long` → `content_version_id` dolu, `invalid` sürüm saklanır | composer unit / Service entegrasyonu; sahte `llm.Client` ham çıktıyı verir (düzenleme yolundaki aynı kodlar HTTP'den E2E'de kapsanır) |
| `internal_error` son `invalid` | httpapi handler unit: sahte `ContentRun` bu olayı yayar; gerçek Service'te zorla üretilmez |
| Heartbeat (`: keepalive`, 15 sn) ve olay biçimi/sırası | httpapi handler unit: sahte `ContentRun` + `clock.Fake` + `httptest` |
| Terminal olay öncesi kayıt: `validated`'dan önce sürüm DB'de | E2E: `validated` alındığında `GET /api/campaigns/{id}` o sürümü `latest_content_version` olarak döner |

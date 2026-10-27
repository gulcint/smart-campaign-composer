# Veritabanı şeması (PostgreSQL 16)

Kapsam: REQ-SND-01, REQ-SND-03, REQ-AUD-04, REQ-APR-03, REQ-SEC-02, REQ-SEED-01/02, REQ-TRK-01/02.
Entegrasyon testleri yalnızca burada adı geçen tablo, kolon, constraint, fonksiyon ve rol adlarına dayanabilir. Kolon sırası ve index'lerin fiziksel ayrıntısı sözleşme değildir; **adı verilen constraint'ler sözleşmedir**.

## 1. Roller ve bağlantılar (REQ-SEC-02)

| Rol | Yetki | Kullanan | Bağlantı ortam değişkeni |
|---|---|---|---|
| `scc_migrator` | `public` şemasının ve tüm nesnelerin sahibi; DDL çalıştırabilir | `cmd/migrate` (ayrı binary, sahip kararı), CI `db-roundtrip` | `MIGRATE_DATABASE_URL` (`secret.Secret` olarak okunur, loglarda maskeli) |
| `scc_app` | Tüm tablolarda yalnızca `SELECT, INSERT, UPDATE, DELETE`; dizilerde `USAGE, SELECT`; fonksiyonlarda `EXECUTE`. `public` şemasında `CREATE` **yok**, hiçbir nesnenin sahibi değil | `cmd/api`, `cmd/worker`, `cmd/seed` | `DATABASE_URL` |

- Roller ve veritabanı (`scc`) Postgres init betiğiyle (süper kullanıcı) oluşturulur; migration değildir. `REVOKE CREATE ON SCHEMA public FROM PUBLIC` uygulanır.
- Varsayılan yetkiler: `ALTER DEFAULT PRIVILEGES FOR ROLE scc_migrator IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO scc_app` (ve diziler/fonksiyonlar için karşılıkları).
- **Gözlemlenebilir davranış:** `scc_app` ile `DROP TABLE customers;`, `CREATE TABLE x (id int);`, `ALTER TABLE campaigns ADD COLUMN x int;`, `TRUNCATE customers;` → SQLSTATE `42501` (`insufficient_privilege`). (`TRUNCATE` yetkisi verilmez; seed silmeyi `DELETE` ile yapar.)

## 2. Migration kuralları

- Dizin: `migrations/`. Ad: `NNNN_snake_case_name.up.sql` + `NNNN_snake_case_name.down.sql`; `NNNN` 4 haneli, 0001'den artan, boşluksuz ve çakışmasız.
- Her `up` için `down` zorunlu; `up → down → up` sonrası şema diff'i boş olmalı (`make db-roundtrip`).
- Takip tablosu: `schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL)` (golang-migrate biçimi).
- Planlanan ilk set (VARSAYIM, adlar bağlayıcı değil): `0001_customers_events`, `0002_customer_stats_fn`, `0003_campaigns_content_approvals`, `0004_deliveries`, `0005_seed_runs`.
- `migrations/embed.go` (`package migrations`, `var FS embed.FS`) dosyaları gömer.

## 3. Tablolar

Tüm `id` kolonları `uuid`; zamanlar `timestamptz` ve UTC yazılır.

### 3.1 `customers`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | uuid | PK (`customers_pkey`); seed'de deterministik |
| `first_name` | text | NOT NULL, `''` olabilir (yedek metin testi); `CHECK (char_length(first_name) <= 100)` |
| `last_name` | text | NOT NULL, `''` olabilir; `CHECK (char_length(last_name) <= 100)` |
| `email` | text | NULL olabilir; `customers_email_fictional_check`: `email IS NULL OR email ~ '^[a-z0-9._+-]+@example\.(com|test)$'` (REQ-SEED-02) |
| `phone` | text | NULL olabilir; E.164; `customers_phone_fictional_check`: `phone IS NULL OR phone ~ '^\+90500000[0-9]{4}$'` (kurgusal blok `+90 500 000 xx xx`) |
| `city` | text | NOT NULL |
| `language` | text | NOT NULL, `CHECK (language IN ('tr','en'))` |
| `loyalty_tier` | text | NOT NULL, `CHECK (loyalty_tier IN ('bronze','silver','gold','platinum'))` |
| `sms_opt_in` | boolean | NOT NULL |
| `sms_opt_in_at` | timestamptz | NULL; `sms_opt_in = false` ise NULL |
| `email_opt_in` | boolean | NOT NULL |
| `email_opt_in_at` | timestamptz | NULL; `email_opt_in = false` ise NULL |
| `created_at` | timestamptz | NOT NULL |
| `last_seen_at` | timestamptz | NULL |

Bilerek **yok**: TC kimlik, doğum tarihi, cinsiyet, adres, ödeme bilgisi (REQ-SEED-02). `email` ve `phone` UNIQUE **değildir** (paylaşılan iletişim bilgisi kenar durumu).

### 3.2 `events`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | uuid | PK; seed'de deterministik |
| `customer_id` | uuid | NOT NULL, FK → `customers(id)` ON DELETE CASCADE |
| `type` | text | NOT NULL, `CHECK (type IN ('cart_add','purchase','page_view'))` |
| `occurred_at` | timestamptz | NOT NULL |
| `amount` | numeric(12,2) | `type='purchase'` ise NOT NULL ve `> 0`, değilse NULL (`events_amount_check`) |
| `product_category` | text | NULL |

Index: `events_customer_type_time_idx (customer_id, type, occurred_at DESC)`.

### 3.3 `customer_stats_at(ref_time)`

SQL fonksiyonu (`LANGUAGE sql STABLE`), segment DSL'in istatistik alanlarının tek kaynağı. Tablo/view değildir; referans zaman parametredir (`now()` kullanmaz).

```sql
customer_stats_at(ref_time timestamptz) RETURNS TABLE (
  customer_id      uuid,
  last_cart_add_at timestamptz,   -- max(occurred_at) where type='cart_add' and occurred_at <= ref_time
  last_purchase_at timestamptz,   -- max(occurred_at) where type='purchase' and occurred_at <= ref_time
  orders_30d       integer,       -- count purchase, occurred_at in [ref_time - 30 days, ref_time]
  cart_adds_30d    integer,       -- count cart_add, same window
  total_orders     integer,       -- count purchase, occurred_at <= ref_time
  total_spent      numeric(14,2)  -- sum(amount) purchase, occurred_at <= ref_time; 0 if none
)
```

Her müşteri için tam bir satır döner (olayı olmayanlar dahil; sayılar 0, zamanlar NULL). "30 gün" = `interval '30 days'` ve alt sınır dahil.

### 3.4 `campaigns`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | uuid | PK |
| `brief` | text | NOT NULL, `campaigns_brief_length_check`: `char_length(brief) BETWEEN 1 AND 2000` |
| `state` | text | NOT NULL, `campaigns_state_check`: `state IN ('draft','audience_ready','content_ready','approved','sending','completed')` |
| `segment_rule` | jsonb | NULL; `{"all":[...]}` biçiminde **kök grup** (gövdedeki `rule` değeri) |
| `segment_reference_time` | timestamptz | NULL |
| `segment_revision` | integer | NOT NULL DEFAULT 0; her başarılı segment üretiminde +1 |
| `segment_llm_attempts` | integer | NULL, 1..2 |
| `segment_generated_at` | timestamptz | NULL |
| `audience_matched`, `audience_sms`, `audience_email` | integer | NULL, ≥ 0 |
| `longest_first_name`, `longest_last_name`, `longest_city` | integer | NULL, ≥ 0; segment üretiminde `content.Longest` (eşleşen küme, izinden önce; `first_name` yedek metin dahil, boş küme → 18/0/0) |
| `approved_content_version_id` | uuid | NULL, FK → `content_versions(id)` |
| `sending_started_at` | timestamptz | NULL |
| `completed_at` | timestamptz | NULL |
| `generation_token` | uuid | NULL; süren segment/içerik üretiminin kira jetonu (D52) |
| `generation_expires_at` | timestamptz | NULL; kiranın bitişi = başlangıç + `MaxGenerationDuration` (servis saati, mikrosaniyeye yukarı yuvarlanır); bu andan sonra kira boş sayılır |
| `created_at`, `updated_at` | timestamptz | NOT NULL |

Durum tutarlılık kısıtları:

- `campaigns_segment_present_check`: `state = 'draft' OR (segment_rule IS NOT NULL AND segment_reference_time IS NOT NULL)`
- `campaigns_approval_present_check`: `(state IN ('approved','sending','completed')) = (approved_content_version_id IS NOT NULL)`
- `campaigns_sending_started_check`: `(state IN ('sending','completed')) = (sending_started_at IS NOT NULL)`
- `campaigns_completed_check`: `(state = 'completed') = (completed_at IS NOT NULL)`
- `campaigns_generation_lease_check`: `(generation_token IS NULL) = (generation_expires_at IS NULL)`

Geçiş tetikleyicisi `campaigns_state_transition_trg` (BEFORE UPDATE OF state): izinli olmayan geçiş `SQLSTATE 23514` (`check_violation`) ile reddedilir. İzinli geçişler (aynı durumda kalmak her zaman serbest):

| Kimden | Kime |
|---|---|
| `draft` | `audience_ready` |
| `audience_ready` | `content_ready` |
| `content_ready` | `audience_ready`, `approved` |
| `approved` | `audience_ready`, `content_ready`, `sending` |
| `sending` | `completed` |
| `completed` | — |

(`approved → completed` doğrudan yok: teslimatsız gönderimde bile aynı işlemde `sending` sonra `completed` yazılır.)

### 3.5 `content_versions`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | uuid | PK |
| `campaign_id` | uuid | NOT NULL, FK → `campaigns(id)` ON DELETE CASCADE |
| `version_number` | integer | NOT NULL, ≥ 1; kampanya başına 1'den artan |
| `segment_revision` | integer | NOT NULL; oluşturulduğu andaki `campaigns.segment_revision` |
| `status` | text | NOT NULL, `CHECK (status IN ('validated','invalid'))` |
| `source` | text | NOT NULL, `CHECK (source IN ('llm','edit'))` |
| `sms_body`, `email_subject`, `email_body` | text | NOT NULL (NFC normalize edilmiş şablon metni) |
| `validation_errors` | jsonb | NOT NULL DEFAULT `'[]'`; `FieldError` dizisi |
| `llm_attempts` | integer | NULL (`source='edit'` ise NULL) |
| `worst_case_sms_runes`, `worst_case_subject_runes` | integer | NULL; şablon söz dizimi geçerliyse oluşturma anındaki segmentin `longest_*` değerleriyle `content.MeasureWorstCase` sonucu |
| `created_at` | timestamptz | NOT NULL |

- `content_versions_campaign_version_key`: `UNIQUE (campaign_id, version_number)`
- `content_versions_id_status_key`: `UNIQUE (id, status)` (onay FK'sı için)
- `content_versions_errors_check`: `(status = 'validated') = (validation_errors = '[]'::jsonb)`

İçerik sürümleri değişmezdir (UPDATE yapılmaz; DELETE yalnız `campaigns` ON DELETE CASCADE ve seed `--reset` ile).

`version_number`: aynı kampanyanın **tüm** satırlarının (`validated` ve `invalid`) `MAX(version_number) + 1`'i (satır yoksa 1), `campaigns` satırı kilitliyken (`SELECT … FOR UPDATE`) hesaplanır; yeniden kullanılmaz ve `segment_revision` değişince sıfırlanmaz. `content_versions_campaign_version_key` yalnız son güvencedir: bu kısıtın SQLSTATE `23505`'i hiçbir zaman HTTP 500'e dönüşmez (kilit altında hesaplandığı için oluşmaması gerekir; oluşursa servis bir kez yeniden dener). "En son sürüm" = güncel `segment_revision`'daki en yüksek `version_number`'lı **`validated`** satır; `invalid` taslaklar hiçbir zaman en son sürüm olmaz.

### 3.6 `approvals`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | uuid | PK |
| `campaign_id` | uuid | NOT NULL, FK → `campaigns(id)` ON DELETE CASCADE |
| `content_version_id` | uuid | NOT NULL |
| `content_status` | text | NOT NULL DEFAULT `'validated'`, `CHECK (content_status = 'validated')` |
| `approved_at` | timestamptz | NOT NULL |
| `revoked_at` | timestamptz | NULL |
| `revoke_reason` | text | NULL, `CHECK (revoke_reason IN ('content_changed','segment_changed'))`; `revoked_at` ile birlikte dolu/boş |

- `approvals_content_version_fk`: `FOREIGN KEY (content_version_id, content_status) REFERENCES content_versions(id, status)` → `invalid` bir sürümün onay kaydı **DB düzeyinde** oluşamaz.
- `approvals_one_active_idx`: `UNIQUE (campaign_id) WHERE revoked_at IS NULL` → kampanya başına en fazla bir etkin onay.
- Değişmez: `campaigns.approved_content_version_id` dolu ⇔ kampanya `approved`/`sending`/`completed` ⇔ etkin (iptal edilmemiş) bir `approvals` satırı vardır ve `content_version_id` değeri aynıdır. İkisi hep aynı transaction'da yazılır. `sending`/`completed` kampanyada onay satırı asla iptal edilmez.

### 3.7 `deliveries` (kuyruğun kendisi)

| Kolon | Tip | Kısıt / anlam |
|---|---|---|
| `id` | uuid | PK; provider'a giden `Idempotency-Key` değeri |
| `campaign_id` | uuid | NOT NULL, FK → `campaigns(id)` |
| `content_version_id` | uuid | NOT NULL, FK → `content_versions(id)`; onaylanan sürüm |
| `customer_id` | uuid | NOT NULL, FK → `customers(id)` |
| `channel` | text | NOT NULL, `CHECK (channel IN ('sms','email'))` |
| `status` | text | NOT NULL DEFAULT `'queued'`, `CHECK (status IN ('queued','sending','sent','failed'))` |
| `attempts` | integer | NOT NULL DEFAULT 0; claim sayısı (işleme denemesi; her claim'de +1, provider'a istek gitmese bile) |
| `failed_attempts` | integer | NOT NULL DEFAULT 0, `CHECK (failed_attempts BETWEEN 0 AND 5)`; 5xx/408/timeout/bağlantı hatası sayısı |
| `rate_limited_count` | integer | NOT NULL DEFAULT 0, `CHECK (rate_limited_count BETWEEN 0 AND 20)`; alınan 429 sayısı |
| `retry_count` | integer | NOT NULL DEFAULT 0; planlanan yeniden deneme sayısı (her neden) |
| `next_attempt_at` | timestamptz | NOT NULL; `queued` satır bu zamandan sonra alınabilir |
| `lease_expires_at` | timestamptz | NULL; `status='sending'` iken NOT NULL |
| `first_attempt_at`, `last_attempt_at` | timestamptz | NULL |
| `last_http_status` | integer | NULL |
| `last_error` | text | NULL; kapalı küme, PII içermez: `http_<status>` (2xx dışı her HTTP yanıtı), `timeout`, `connection_error`, `content_too_long`, `recipient_unavailable` |
| `failure_reason` | text | NULL; `CHECK (failure_reason IN ('provider_rejected','max_attempts_exceeded','rate_limit_budget_exhausted','content_too_long','recipient_unavailable'))` |
| `provider_message_id` | text | NULL |
| `sent_at`, `failed_at` | timestamptz | NULL |
| `created_at`, `updated_at` | timestamptz | NOT NULL |

Adlandırılmış kısıtlar (sözleşme):

- **`deliveries_campaign_channel_customer_key`: `UNIQUE (campaign_id, channel, customer_id)`** — REQ-SND-03. İkinci kayıt girişimi SQLSTATE `23505` ile reddedilir.
- `deliveries_sent_check`: `(status = 'sent') = (sent_at IS NOT NULL)`
- `deliveries_failed_check`: `(status = 'failed') = (failure_reason IS NOT NULL AND failed_at IS NOT NULL)`
- `deliveries_lease_check`: `status <> 'sending' OR lease_expires_at IS NOT NULL`

Teslimat geçişleri: `queued → sending` (claim), `sending → sent`, `sending → failed`, `sending → queued` (retry planlandı veya süresi dolmuş lease geri alındı). Claim'den sonra, provider çağrısından önce iki denetim yapılır (sırayla; go-interfaces `Dispatcher.ProcessOnce`): (1) claim anında yeniden okunan kanal izni `false` veya iletişim (`phone`/`email`) NULL → `sending → failed`, `failure_reason = 'recipient_unavailable'`, `last_error = 'recipient_unavailable'`; (2) render sonrası uzunluk aşımı → `sending → failed`, `failure_reason = 'content_too_long'`, `last_error = 'content_too_long'`. İkisinde de provider'a istek gitmez ve hız sınırlayıcı izni tüketilmez. `queued → failed` geçişi yoktur. `sent` ve `failed` sonuç durumlarıdır, değişmez.

Index'ler: `deliveries_claim_idx (channel, next_attempt_at) WHERE status = 'queued'`; `deliveries_lease_idx (lease_expires_at) WHERE status = 'sending'`; `deliveries_campaign_status_idx (campaign_id, status)`.

**PII:** `deliveries` alıcı adresi saklamaz; worker gönderim anında `customers`'tan okur.

### 3.8 `seed_runs`

| Kolon | Tip | Kısıt |
|---|---|---|
| `id` | integer | PK, `CHECK (id = 1)` (tek satır) |
| `seed` | bigint | NOT NULL |
| `users` | integer | NOT NULL |
| `ref_time` | timestamptz | NOT NULL |
| `dataset_sha256` | text | NOT NULL |
| `loaded_at` | timestamptz | NOT NULL |

### 3.9 Kampanya yazma işlemleri (her biri tek transaction, `campaigns` satırı `FOR UPDATE`)

HTTP katmanında 409 üreten kontroller bu kilit altında, durum değişmeden önce yapılır; DB tetikleyicisi (§3.4) yalnızca son güvencedir.

| İşlem | Aynı transaction'daki yazmalar |
|---|---|
| Segment üretimi (P3) | Satır hâlâ bu üretimin `generation_token`'ını taşımıyorsa hiçbir yazma yok (kira sona erip başkasına geçti; D52). Aksi halde `segment_*`/`audience_*`/`longest_*` yazılır, `segment_revision + 1`; etkin onay varsa `revoked_at`, `revoke_reason = 'segment_changed'` ve `approved_content_version_id = NULL`; durum `audience_ready` |
| `editContent` ön koşulu | Satır kilidi altında, ekleme ile **aynı** transaction'da: `X-Expected-Version` = güncel `segment_revision`'daki en yüksek `version_number`'lı `validated` satırın `id`'si (satır yoksa `none`) değilse hiçbir yazma yapılmaz ve 409 `content_edit_conflict` döner (karşılaştırma ile ekleme arasına başka yazma giremez) |
| İçerik `validated` (LLM `validated` olayı veya `editContent`) | yeni `content_versions` satırı (`status = 'validated'`, `source` = `llm`/`edit`, `segment_revision` = güncel, `worst_case_*` dolu, `validation_errors = '[]'`); durum `content_ready`; etkin onay varsa `revoke_reason = 'content_changed'` ve `approved_content_version_id = NULL` |
| Saklanabilir son `invalid` taslak (LLM) | yalnız yeni `content_versions` satırı (`status = 'invalid'`, `source = 'llm'`, `validation_errors` dolu); `campaigns` ve `approvals` satırlarına dokunulmaz |
| Diğer son `invalid` sonuçları, kopan akış, 422 ile reddedilen düzenleme | hiçbir yazma yok |
| Onay | yeni `approvals` satırı (`content_status = 'validated'`), `approved_content_version_id` = sürüm, durum `approved` |
| Aynı sürümün tekrar onayı (idempotent) | hiçbir yazma yok (`approved_at` değişmez) |
| Üretim başlangıcı (segment veya içerik) | Durum ve kira aynı kilit altında denetlenir; süresi geçmemiş kira varsa 409 `generation_in_progress`, yoksa `generation_token`/`generation_expires_at` yazılır (süresi geçmiş kiranın üzerine). `updated_at` değişmez |
| LLM içerik sürümünün kaydı | Satır hâlâ bu üretimin `generation_token`'ını taşımıyorsa hiçbir yazma yok (son `invalid` olayı, `llm_unavailable`) |
| Üretim sonu (başarı, hata, istemci kopması) | `generation_token = NULL, generation_expires_at = NULL WHERE generation_token = <kendi jetonu>` (tek ifade; başkasının kirasına dokunmaz) |
| Gönderim (P5) | §3.7 / README ve openapi `sendCampaign` |

Düzenleme, onay ve gönderim, kendi transaction'larında satırı kilitledikten sonra, durum denetimlerinden önce süresi geçmemiş bir kira olup olmadığına bakar (`LockedCampaign.CheckNoGeneration`); varsa hiçbir yazma yapmadan 409 `generation_in_progress` döner. Onay ve gönderimde bundan önce yalnız sürümün varlığı (404 `content_version_not_found`) denetlenir; düzenlemede kira denetimi ilk denetimdir. Böylece aynı veritabanını kullanan birden fazla API sürecinde de üretim başlangıcı ile gönderimden yalnız biri kabul edilir (REQ-SND-01).

## 4. Kuyruk mekanikleri (gözlemlenebilir)

- **Claim** (müşteri satırı aynı sorguda `customers` ile birleştirilerek **yeniden okunur**: güncel `phone`/`email` ve kanal izni `delivery.Job.To`/`Consent`'e girer): `UPDATE deliveries SET status='sending', lease_expires_at=$lease, attempts=attempts+1, last_attempt_at=$now, first_attempt_at=COALESCE(first_attempt_at,$now) WHERE id IN (SELECT id FROM deliveries WHERE channel=$ch AND status='queued' AND next_attempt_at <= $now ORDER BY next_attempt_at, id LIMIT $n FOR UPDATE SKIP LOCKED) RETURNING ...` → eşzamanlı worker'lar aynı satırı alamaz.
- **Lease geri alma:** `status='sending' AND lease_expires_at < $now` olan satırlar `queued`'a döner (`failed_attempts` artmaz). Yeniden gönderim aynı `Idempotency-Key` (= `id`) ile yapılır; provider tekilleştirir. Lease süresi `DELIVERY_LEASE` (varsayılan `60s`).
- **Kanal başına tek dispatcher:** `pg_try_advisory_lock(key)` oturum kilidi. Anahtarlar: `sms` = `7001`, `email` = `7002` (bigint). Kilidi tutmayan worker o kanal için istek başlatmaz.
- **Canlı durum:** `LISTEN/NOTIFY` kullanılmaz; API'nin durum akışı `Service.Status`'u 1 sn'de bir yoklar (go-interfaces §httpapi). VARSAYIM.
- **Tamamlama:** sonuç durumuna geçen her teslimattan sonra aynı işlemde: `UPDATE campaigns SET state='completed', completed_at=$now WHERE id=$c AND state='sending' AND NOT EXISTS (SELECT 1 FROM deliveries WHERE campaign_id=$c AND status IN ('queued','sending'))`.

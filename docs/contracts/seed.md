# Seed (`cmd/seed`) ve manifest

Kapsam: REQ-SEED-01, REQ-SEED-02, REQ-AUD-00, REQ-AUD-03, REQ-AUD-04, REQ-CNT-03/05 (kenar durumları), REQ-SND-03 (paylaşılan iletişim), REQ-DLV-01.
Go: [`go-interfaces.md` §seed](go-interfaces.md#internalseed). Tablolar: [`db-schema.md`](db-schema.md).

## 1. CLI

```text
seed [--seed N] [--users N] [--ref-time T] [--manifest PATH] [--export PATH]
     [--db-url URL] [--skip-db] [--reset | --if-empty]
```

| Bayrak | Tip | Varsayılan | Anlam |
|---|---|---|---|
| `--seed` | int64 | `42` | Rastgelelik tohumu |
| `--users` | int | `5000` | Müşteri sayısı (`200`–`9999`, yoksa çıkış kodu 2; üst sınır: kurgusal telefon bloğunda müşteri başına tekil numara) |
| `--ref-time` | RFC 3339 veya `now` | `now` | Referans zaman; saniyeye yuvarlanır, UTC. Olaylar bu zamana göre geçmişe yerleştirilir; `ref`'ten sonra olay üretilmez |
| `--manifest` | yol | `testdata/seed/manifest.json` | Manifestin yazılacağı yer (`-` = stdout). Her çalıştırmada yazılır |
| `--export` | yol | (yok) | Verilirse veri kümesi JSONL olarak yazılır (§4) |
| `--db-url` | URL | `$DATABASE_URL` | Hedef veritabanı (`scc_app` rolü yeterli) |
| `--skip-db` | bool | `false` | DB'ye hiç bağlanmaz; yalnızca manifest/export üretir (unit testler ve determinizm kontrolü için) |
| `--reset` | bool | `false` | DB'de farklı bir veri kümesi varsa önce siler (`deliveries`, `approvals`, `content_versions`, `campaigns`, `events`, `customers`) |
| `--if-empty` | bool | `false` | `seed_runs`'ta herhangi bir kayıt varsa DB'ye dokunmadan çıkış kodu `0` ile biter (manifest yine yazılır). `--reset` ile birlikte verilemez (çıkış kodu 2) |

Çıkış kodları: `0` başarı · `1` çalışma hatası (DB, `ErrDatasetMismatch`) · `2` kullanım hatası (geçersiz bayrak/değer, `--users` `200`–`9999` dışında, `--reset` ile `--if-empty` birlikte, `--skip-db` olmadan boş `--db-url`).

Compose: `seed` servisi `migrate` tamamlanınca `--seed=42 --users=5000 --ref-time=now --if-empty` ile tek seferlik çalışır (boş volume'da yükler; sonraki `docker compose up`'larda veriye ve kampanyalara dokunmaz). Bugüne göre yeniden seed bilinçli ve elle yapılır: `docker compose run --rm seed seed --ref-time=now --reset --manifest=/tmp/manifest.json` (kampanyaları ve teslimatları siler); `api` ve `worker` `seed` başarıyla bitince başlar. `compose.test.yml`: `--seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z` ve api'de `SEGMENT_REFERENCE_TIME=2026-01-15T12:00:00Z`.

## 2. Determinizm ve idempotentlik (REQ-SEED-01)

- Aynı (`--seed`, `--users`, `--ref-time`) → **bayt bayt aynı** manifest ve export dosyası (`seed.Generate` saf fonksiyondur: saat, global rand, map sırası, ortam kullanılmaz).
- Kimlikler deterministik UUID'dir: `customers.id = UUIDv5(NS, "scc-seed/<seed>/customer/<index>")`, `events.id = UUIDv5(NS, "scc-seed/<seed>/event/<customer_index>/<n>")`, `NS = 3f6c1b52-8a3e-5d2f-9b7c-2e4d6a8f0c11`.
- DB'ye yükleme tek transaction'da upsert'tir; aynı parametrelerle tekrar çalıştırma kayıt **çoğaltmaz** (`SELECT count(*) FROM customers` = `--users`, `events` sayısı = `totals.events`). `seed_runs` tek satırında `dataset_sha256` tutulur; farklı veri kümesi + `--reset` yoksa çıkış kodu 1.
- Manifest `testdata/seed/manifest.json`, **`--seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z`** ile üretilmiş haliyle repoda bulunur. Testler sayıları **yalnızca** bu dosyadan okur, kendi hesapladığı sayıyı sabit yazmaz.
- Manifest sayıları üretici tarafından `segment.Match` ile Go'da hesaplanır; entegrasyon testi aynı kuralın SQL sonucunu (`segment.Counter` / HTTP) manifestle karşılaştırır — iki bağımsız yolun çapraz kontrolü.

## 3. Veri kuralları

### 3.1 Kurgusal veri (REQ-SEED-02)

| Alan | Kural |
|---|---|
| `email` | Yalnızca `@example.com` (%90) veya `@example.test` (%10). Biçim: `<ad-ascii>.<soyad-ascii>.<index>@example.com`, küçük harf, Türkçe harfler ASCII'ye çevrilir (`ş→s`, `ğ→g`, `ı→i`, `İ→i`, `ö→o`, `ü→u`, `ç→c`) |
| `phone` | E.164, kurgusal blok `+90500000XXXX` (gösterim `+90 500 000 xx xx`). `+905000009999` paylaşılan çift için ayrılmıştır; diğerleri tekildir |
| `first_name`, `last_name` | Küratörlü yaygın Türkçe ad/soyad listelerinden rastgele birleşim (gerçek bir kişiyi hedeflemez) |
| Olmayan alanlar | TC kimlik, doğum tarihi, cinsiyet, adres, ödeme bilgisi **yoktur** |

### 3.2 Rastgele nüfus (sabit gruplar dışındaki müşteriler)

| Özellik | Dağılım |
|---|---|
| `city` | `İstanbul` %30; kalan %70 eşit olarak `Ankara`, `İzmir`, `Bursa`, `Antalya`, `Adana`, `Konya`, `Gaziantep`, `Kayseri`, `Eskişehir` |
| `language` | `tr` %90, `en` %10 |
| `loyalty_tier` | `bronze` %50, `silver` %30, `gold` %15, `platinum` %5 |
| İzin | ikisi %50, yalnız SMS %15, yalnız email %15, hiçbiri %20 |
| İletişim | `phone` NULL %5, `email` NULL %3 (izinden bağımsız) |
| `first_name` | boş değil, ≤ 20 karakter |
| Olaylar | Müşteri başına 0–12 olay, `[ref − 120 gün, ref − 1 saat]` aralığında saniye hassasiyetinde; `cart_add` %45, `page_view` %40, `purchase` %15; `purchase.amount` 50.00–5000.00 TRY |

### 3.3 Sabit kenar grupları

Sabit gruplar index 0'dan başlayarak **önce** üretilir; sayıları `--users`'tan bağımsızdır. Aşağıda "eşleşir" = örnek brief kuralı ([`segment-dsl.md` §7.1](segment-dsl.md#71-örnek-brief-kuralı-kanonik)) bu müşteriyi seçer. Aksi belirtilmedikçe: `city = İstanbul`, ikisi de izinli, `phone` ve `email` dolu, `first_name` ≤ 20 karakter, hiç `purchase` yok.

| Grup adı | Adet | Ayırt edici veri | Eşleşir | SMS kitlesi | Email kitlesi |
|---|---|---|---|---|---|
| `cart_add_29d` | 5 | Tek `cart_add`: tam `ref − 29×24h` | evet | evet | evet |
| `cart_add_30d` | 5 | Tek `cart_add`: tam `ref − 30×24h` (alt sınır dahil) | evet | evet | evet |
| `cart_add_31d` | 5 | Tek `cart_add`: tam `ref − 31×24h` | hayır | hayır | hayır |
| `cart_add_then_purchase` | 10 | `cart_add` `ref − 10 gün`, `purchase` `ref − 5 gün` | hayır | hayır | hayır |
| `purchase_before_cart_add` | 6 | `purchase` `ref − 20 gün`, sonra `cart_add` `ref − 10 gün`. Kabul edilen yorumda (son 30 günde satın alma yok) **dışlanır**; "sepete eklemeden sonra satın almadı" yorumunda dahil olurdu → iki yorum farklı sayı verir | hayır | hayır | hayır |
| `istanbul_no_consent` | 10 | `cart_add` `ref − 7 gün`; `sms_opt_in=false`, `email_opt_in=false` | evet | hayır | hayır |
| `istanbul_sms_only` | 8 | `cart_add` `ref − 7 gün`; yalnız SMS izni | evet | evet | hayır |
| `istanbul_email_only` | 8 | `cart_add` `ref − 7 gün`; yalnız email izni | evet | hayır | evet |
| `sms_consent_missing_phone` | 4 | `cart_add` `ref − 7 gün`; `sms_opt_in=true`, `phone=NULL`, `email_opt_in=false` | evet | hayır | hayır |
| `email_consent_missing_email` | 4 | `cart_add` `ref − 7 gün`; `email_opt_in=true`, `email=NULL`, `sms_opt_in=false` | evet | hayır | hayır |
| `empty_first_name` | 6 | `cart_add` `ref − 7 gün`; `first_name = ''` | evet | evet | evet |
| `long_first_name` | 3 | `cart_add` `ref − 7 gün`; `first_name = "Şükriye Gülizar Çağla Ayşegül İpek Öykü Nilüfer Su"` (tam 50 karakter) | evet | evet | evet |
| `shared_contact` | 2 | `cart_add` `ref − 7 gün`; ikisi de `phone = "+905000009999"`, `email = "shared.contact@example.test"` | evet | evet | evet |
| `non_istanbul_cart_add` | 10 | `city = Ankara`, `cart_add` `ref − 3 gün` | hayır | hayır | hayır |

Garantiler: veri kümesindeki en uzun `first_name` 50 karakterdir (yalnız `long_first_name`); `+905000009999` ve `shared.contact@example.test` yalnız `shared_contact` grubunda geçer.

## 4. Export biçimi (`--export`)

UTF-8 JSONL; önce tüm müşteriler (`id` artan), sonra tüm olaylar (`id` artan). Her satır kompakt JSON, anahtarlar aşağıdaki sırada, satır sonu `\n`:

```json
{"kind":"customer","id":"…","first_name":"…","last_name":"…","email":"…|null","phone":"…|null","city":"…","language":"tr","loyalty_tier":"bronze","sms_opt_in":true,"sms_opt_in_at":"…|null","email_opt_in":true,"email_opt_in_at":"…|null","created_at":"…","last_seen_at":"…|null"}
{"kind":"event","id":"…","customer_id":"…","type":"cart_add","occurred_at":"2026-01-08T12:00:00Z","amount":null,"product_category":"…|null"}
```

`amount` JSON sayısı olarak 2 ondalıkla (`149.90`) yazılır. `dataset_sha256` = bu dosyanın baytlarının SHA-256'sı (küçük harf hex).

## 5. `manifest.json` şeması

Biçim: 2 boşluk girinti, anahtarlar aşağıdaki sırada, sonda tek `\n`. Grup `customer_ids` dizileri artan sıralıdır.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SeedManifest",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "params", "dataset_sha256", "totals", "consent", "contact", "reachable", "cities", "example_brief", "groups"],
  "properties": {
    "schema_version": { "const": 1 },
    "params": {
      "type": "object", "additionalProperties": false, "required": ["seed", "users", "ref_time"],
      "properties": {
        "seed": { "type": "integer" },
        "users": { "type": "integer", "minimum": 200 },
        "ref_time": { "type": "string", "format": "date-time" }
      }
    },
    "dataset_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
    "totals": {
      "type": "object", "additionalProperties": false, "required": ["customers", "events", "events_by_type"],
      "properties": {
        "customers": { "type": "integer" },
        "events": { "type": "integer" },
        "events_by_type": {
          "type": "object", "additionalProperties": false, "required": ["cart_add", "purchase", "page_view"],
          "properties": { "cart_add": { "type": "integer" }, "purchase": { "type": "integer" }, "page_view": { "type": "integer" } }
        }
      }
    },
    "consent": { "$ref": "#/$defs/consentBreakdown", "description": "All customers, by opt-in flags only" },
    "contact": {
      "type": "object", "additionalProperties": false, "required": ["missing_phone", "missing_email"],
      "properties": { "missing_phone": { "type": "integer" }, "missing_email": { "type": "integer" } }
    },
    "reachable": { "$ref": "#/$defs/audience", "description": "All customers: matched = customers, sms/email = consent AND contact present" },
    "cities": { "type": "object", "additionalProperties": { "type": "integer" }, "description": "customer count per city" },
    "example_brief": {
      "type": "object", "additionalProperties": false,
      "required": ["brief", "rule", "audience", "consent", "longest_values"],
      "properties": {
        "brief": { "const": "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun." },
        "rule": { "description": "Exactly segment-dsl.md §7.1 'rule' value (root group)", "type": "object" },
        "audience": { "$ref": "#/$defs/audience", "description": "Expected GET/POST audience for the rule at params.ref_time" },
        "consent": { "$ref": "#/$defs/consentBreakdown", "description": "Matched customers, by opt-in flags only" },
        "longest_values": {
          "type": "object", "additionalProperties": false, "required": ["first_name", "last_name", "city"],
          "description": "Max rune length over MATCHED customers (before consent); first_name uses EffectiveFirstName (empty => 'Değerli müşterimiz', 18)",
          "properties": { "first_name": { "type": "integer" }, "last_name": { "type": "integer" }, "city": { "type": "integer" } }
        }
      }
    },
    "groups": {
      "type": "object",
      "required": ["cart_add_29d", "cart_add_30d", "cart_add_31d", "cart_add_then_purchase", "purchase_before_cart_add", "istanbul_no_consent", "istanbul_sms_only", "istanbul_email_only", "sms_consent_missing_phone", "email_consent_missing_email", "empty_first_name", "long_first_name", "shared_contact", "non_istanbul_cart_add"],
      "propertyNames": { "enum": ["cart_add_29d", "cart_add_30d", "cart_add_31d", "cart_add_then_purchase", "purchase_before_cart_add", "istanbul_no_consent", "istanbul_sms_only", "istanbul_email_only", "sms_consent_missing_phone", "email_consent_missing_email", "empty_first_name", "long_first_name", "shared_contact", "non_istanbul_cart_add"] },
      "additionalProperties": { "$ref": "#/$defs/group" }
    }
  },
  "$defs": {
    "audience": {
      "type": "object", "additionalProperties": false, "required": ["matched", "sms", "email"],
      "properties": { "matched": { "type": "integer" }, "sms": { "type": "integer" }, "email": { "type": "integer" } }
    },
    "consentBreakdown": {
      "type": "object", "additionalProperties": false, "required": ["both", "sms_only", "email_only", "none"],
      "properties": { "both": { "type": "integer" }, "sms_only": { "type": "integer" }, "email_only": { "type": "integer" }, "none": { "type": "integer" } }
    },
    "group": {
      "type": "object", "additionalProperties": false,
      "required": ["count", "customer_ids", "matches_example_brief", "in_sms_audience", "in_email_audience"],
      "properties": {
        "count": { "type": "integer" },
        "customer_ids": { "type": "array", "items": { "type": "string", "format": "uuid" } },
        "matches_example_brief": { "type": "boolean" },
        "in_sms_audience": { "type": "boolean" },
        "in_email_audience": { "type": "boolean" }
      }
    }
  }
}
```

Grup değerleri §3.3 tablosuyla birebir aynıdır (`count`, `matches_example_brief`, `in_sms_audience`, `in_email_audience`). Testlerin tipik kullanımı:

- `example_brief.audience` = `POST /api/campaigns/{id}/segment` yanıtındaki `audience` (REQ-AUD-00, REQ-AUD-03) = `POST /send` yanıtındaki `deliveries.sms`/`deliveries.email` (REQ-SND-01).
- `groups.istanbul_no_consent.customer_ids` için hiç teslimat yok; `groups.istanbul_sms_only` için yalnız `sms` (REQ-AUD-04).
- `groups.cart_add_30d` teslimat alır, `groups.cart_add_31d` almaz (sınır).
- `groups.purchase_before_cart_add` teslimat almaz ("alışveriş yapmayan" = son 30 günde satın alma yok; segment-dsl §7.1).
- `groups.shared_contact.customer_ids` (2 kişi) her biri bir `sms` ve bir `email` teslimatı alır (REQ-SND-03).
- `example_brief.longest_values` = `POST /segment` yanıtındaki `segment.longest_values`; `first_name` = 50 → içerik doğrulamasında ve onayda en kötü durum hesabı (REQ-CNT-01, REQ-CNT-05).
- `groups.empty_first_name` önizlemesinde `Değerli müşterimiz` (REQ-CNT-03).

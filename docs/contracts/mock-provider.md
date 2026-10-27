# Mock provider (`cmd/mockprovider`)

Kapsam: REQ-MOCK-01, REQ-MOCK-02, REQ-SND-02, REQ-SND-03, REQ-SND-04, REQ-SND-05.
Go: [`go-interfaces.md` §mockprovider, §provider](go-interfaces.md#internalmockprovider).

Gerçek bir SMS/email servisini taklit eden bağımsız HTTP servisi. Compose'da yalnızca iç ağdadır (port yayınlanmaz). Worker'ın `PROVIDER_BASE_URL`'i varsayılan olarak `http://mockprovider:8090`'dır; gerçek bir SMS/email servisine bağlanan bileşen yoktur (REQ-SND-05).

## 1. Gönderim uç noktaları

| Metot | Yol | Gövde |
|---|---|---|
| `POST` | `/v1/sms/messages` | `{"to": "+905000001234", "body": "…"}` |
| `POST` | `/v1/email/messages` | `{"to": "a.b.12@example.com", "subject": "…", "text": "…", "html": "<!doctype html>…"}` |

İstek başlıkları: `Content-Type: application/json` (zorunlu), **`Idempotency-Key: <delivery uuid>`** (zorunlu).

### 1.1 Yanıtlar

| Durum | Gövde | Başlıklar | Ne zaman |
|---|---|---|---|
| `202 Accepted` | `{"id":"msg_<32 hex>","status":"accepted","channel":"sms","idempotency_key":"…","duplicate":false}` | — | Mesaj kabul edildi |
| `202 Accepted` (tekrar) | İlk kabulün **aynı** gövdesi, yalnız `"duplicate": true` | `Idempotent-Replayed: true` | Aynı `Idempotency-Key` daha önce **kabul edilmişse**; mesaj tekrar kabul edilmez, kayıt sayısı artmaz (REQ-MOCK-02) |
| `400 Bad Request` | `{"error":{"code":"missing_idempotency_key"|"invalid_body"|"invalid_recipient","message":"…"}}` | — | Başlık yok; JSON bozuk/eksik alan veya `Content-Type` JSON değil (`invalid_body`); senaryolu `status: 400` (`invalid_body`); alıcı kurgusal değil (`to` SMS'te `^\+90500000[0-9]{4}$`, email'de `@example.com`/`@example.test` değilse) |
| `408 Request Timeout` | `{"error":{"code":"request_timeout","message":"…"}}` | — | Yalnız senaryolu `status: 408` |
| `409 Conflict` | `{"error":{"code":"idempotency_key_reused","message":"…"}}` | — | Aynı anahtar, farklı kanal veya farklı `to` ile geldi |
| `429 Too Many Requests` | `{"error":{"code":"rate_limited","message":"…"}}` | `Retry-After: <MOCK_PROVIDER_RETRY_AFTER_SECONDS>` (tam sayı saniye) | Kanal hız sınırı aşıldı (§2) veya rastgele/senaryolu 429 |
| `500` / `503` | `{"error":{"code":"internal_error","message":"…"}}` | — | Rastgele veya senaryolu hata |
| (yanıt yok) | — | — | "Yanıt kayboldu": mesaj **kabul edilip kaydedilir**, ardından bağlantı yanıt yazılmadan kapatılır. İstemci `provider.ErrConnection` görür; aynı anahtarla tekrar gelen istek `202` + `duplicate:true` alır |

- Yalnızca `202` sonuçları idempotency deposuna yazılır. `4xx`/`5xx`/`429` sonuçları saklanmaz; aynı anahtarla sonraki istek yeniden değerlendirilir (aksi halde retry anlamsız olurdu). VARSAYIM.
- İşlem sırası: (1) gövde/başlık doğrulama → (2) idempotency deposunda varsa replay → (3) **senaryo kuyruğuna bakılır** (§4): eşleşen öğe varsa tüketilir ve doğrudan (6)'ya geçilir — hız sınırı ve rastgele karar **atlanır** → (4) kanal hız sınırı → (5) rastgele hata kararı → (6) gecikme (senaryo öğesinin `delay_ms`'i veya rastgele) → (7) yanıt.
- Replay (2) hız sınırına, senaryoya ve hata enjeksiyonuna tabi değildir ve gecikme uygulanmaz.

## 2. Kendi hız sınırı (REQ-MOCK-02)

- Kanal başına kayan pencere: bir istek adım (4)'e ulaştığında, aynı kanalda son 1 saniyede (`(now − 1s, now]`) adım (4)'e ulaşmış (kabul veya ret fark etmeksizin) istek sayısı ≥ `MOCK_PROVIDER_RATE_LIMIT_PER_SECOND` (varsayılan 100) ise → `429` + `Retry-After`. Senaryo öğesi tüketen istekler bu sayıma girmez.
- SMS ve email sayaçları bağımsızdır.

## 3. Ortam değişkenleri

| Değişken | Tip | Varsayılan | Anlam |
|---|---|---|---|
| `MOCK_PROVIDER_ADDR` | string | `:8090` | Dinleme adresi |
| `MOCK_PROVIDER_SEED` | int64 | `1` | Rastgele kararların tohumu |
| `MOCK_PROVIDER_ERROR_RATE` | float [0,1] | `0.05` | 5xx olasılığı (`500` ve `503` eşit) |
| `MOCK_PROVIDER_RATE_LIMIT_RATE` | float [0,1] | `0.02` | Hız sınırından bağımsız rastgele 429 olasılığı |
| `MOCK_PROVIDER_LOST_RESPONSE_RATE` | float [0,1] | `0` | "Kabul et, yanıtı kaybet" olasılığı |
| `MOCK_PROVIDER_LATENCY_MIN` | duration | `20ms` | Yanıt gecikmesi alt sınırı |
| `MOCK_PROVIDER_LATENCY_MAX` | duration | `200ms` | Üst sınır (uniform) |
| `MOCK_PROVIDER_RATE_LIMIT_PER_SECOND` | int | `100` | Kanal başına kendi sınırı |
| `MOCK_PROVIDER_RETRY_AFTER_SECONDS` | int | `1` | 429'da `Retry-After` |
| `MOCK_PROVIDER_ADMIN` | bool | `false` | `/admin/*` uç noktalarını açar (`compose.test.yml`'de `true`) |

Geçersiz değer → süreç çıkış kodu 1 (`mockprovider.ErrInvalidConfig`). Oranların toplamı 1'i aşamaz.

**Determinizm:** Her kanal için ayrı `*rand.Rand` = `rand.New(rand.NewPCG(uint64(seed), channelSalt))`, `channelSalt`: sms=`1`, email=`2`. Her rastgele değerlendirmede (adım 5) sırasıyla tek bir uniform sayı ile hata türü (`error` / `rate_limit` / `lost_response` / `ok`), ardından bir sayı ile gecikme çekilir. Aynı tohum + aynı kanal içi istek varış sırası → aynı karar dizisi. (Eşzamanlı isteklerde varış sırası ağ zamanlamasına bağlıdır; birebir dizi gereken testler §4'ü kullanır.)

## 4. Senaryolu (scripted) mod — yalnız `MOCK_PROVIDER_ADMIN=true`

Testlerin `429, 500, 202` gibi tam dizileri zorlaması için. Admin kapalıyken `/admin/*` → `404`.

| Metot | Yol | Gövde / yanıt |
|---|---|---|
| `POST` | `/admin/scripts` | İstek: `{"channel":"sms","to":"+905000001234","responses":[{"status":429,"retry_after":"2"},{"status":500},{"status":202}]}` → `204`. `to` isteğe bağlıdır |
| `DELETE` | `/admin/scripts` | Tüm senaryoları siler → `204` |
| `GET` | `/admin/messages` | `200 {"items":[{"id","channel","to","idempotency_key","accepted_at","request_count"}]}` — kabul edilmiş tekil mesajlar (`accepted_at` artan); `request_count` = o anahtarla gelen **her** istek; doğrulamadan önce sayılır, sonucu ne olursa olsun (4xx/5xx/429/kayıp yanıt/replay/kabul) dahildir |
| `GET` | `/admin/requests` | `200 {"items":[{"seq","channel","idempotency_key","to","received_at","status","replayed"}]}` — değerlendirilen her istek, varış sırasıyla (`seq` 1'den); `status` dönülen HTTP kodu, kayıp yanıtta `0`; `POST /admin/reset` kaydı temizlediği için `seq` yeniden 1'den başlar |
| `POST` | `/admin/reset` | Mesajlar, istek kaydı, idempotency deposu, hız penceresi ve senaryolar temizlenir; rastgele üreteçler tohumdan yeniden kurulur → `204` |

Senaryo yanıt öğesi: `{"status": 202|400|408|429|500|503, "retry_after": "<header değeri, isteğe bağlı>", "delay_ms": <int, isteğe bağlı, varsayılan 0>, "lost_response": <bool, isteğe bağlı>}`. `status: 202` gerçek kabul ve kayıt yapar; `lost_response: true` yalnızca `202` ile anlamlıdır (kabul + yanıtı kaybet).

Eşleşme (§1.1 adım 3, hız sınırından **önce**): önce `channel` + `to` eşleşen kuyruğun, yoksa yalnız `channel` kuyruğunun (`to` verilmemiş) ilk öğesi tüketilir (FIFO). Senaryo öğesi tüketen isteğe hız sınırı **uygulanmaz** ve rastgele karar çekilmez; bu yüzden `429, 500, 202` dizisi her zaman birebir üretilir. Kuyruk boşsa §1.1 adım 4'ten devam edilir.

## 5. Diğer

- `GET /healthz` → `200 {"status":"ok"}`.
- Mock provider log'ları `to`, gövde veya konu içermez; yalnızca kanal, `idempotency_key`, durum kodu.
- Compose: `mockprovider` servisi; portu dışarı yayınlanmaz (REQ-DLV-01).

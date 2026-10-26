# Metrics ve health

Kapsam: REQ-TRK-02, REQ-TRK-03, REQ-SND-04 (gözlem).
Biçim: Prometheus text exposition (`Content-Type: text/plain; version=0.0.4; charset=utf-8`), `prometheus/client_golang`. Tüm adların öneki `scc_`.

## 1. API süreci — `GET /metrics` (`cmd/api`, port 8080)

REQ-TRK-02'nin test edildiği uç nokta budur. Teslimat serileri **her scrape'te veritabanından** (`deliveries` tablosu) türetilir; böylece hangi worker süreci işlemiş olursa olsun tutarlıdır. Etiket değerleri kapalı kümedir; hiç teslimat olmasa bile her `channel` (ve gerekli `reason`) kombinasyonu `0` değeriyle yayımlanır.

| Ad | Tip | Etiketler | Değer / ne zaman değişir |
|---|---|---|---|
| `scc_deliveries_sent_total` | counter | `channel` (`sms`,`email`) | `status='sent'` teslimat sayısı; bir teslimat `sent` olunca +1 |
| `scc_deliveries_failed_total` | counter | `channel`, `reason` (`provider_rejected`,`max_attempts_exceeded`,`rate_limit_budget_exhausted`,`content_too_long`,`recipient_unavailable`) | `status='failed'` teslimat sayısı; teslimat `failed` olunca ilgili `reason`'da +1 |
| `scc_delivery_retries_total` | counter | `channel` | `sum(deliveries.retry_count)`; her planlanan yeniden denemede (5xx/408/timeout/bağlantı **ve** 429) +1 |
| `scc_deliveries_queued` | gauge | `channel` | `status='queued'` teslimat sayısı (retry bekleyenler dahil); gönderim isteğiyle artar, claim ile azalır |
| `scc_deliveries_in_flight` | gauge | `channel` | `status='sending'` teslimat sayısı |
| `scc_campaigns` | gauge | `state` (6 durum) | Durum başına kampanya sayısı |
| `scc_http_requests_total` | counter | `method`, `route` (şablon, ör. `/api/campaigns/{campaign_id}/send`), `status` | Her HTTP yanıtında +1 |
| `scc_http_request_duration_seconds` | histogram | `method`, `route` | Yanıt süresi (SSE uç noktaları hariç) |
| `scc_llm_requests_total` | counter | `operation` (`segment`,`content`), `provider` (`mock`,`claude`), `outcome` (`valid`,`invalid`,`unavailable`) | Her LLM **denemesinde** +1 |
| `scc_llm_repairs_total` | counter | `operation` | Her düzeltme denemesi (deneme 2) başlatıldığında +1 |

Not: DB'den türetilen "counter"lar yalnızca seed `--reset` ile veri silinirse azalır (Prometheus bunu reset olarak yorumlar); VARSAYIM, `DECISIONS.md`'ye yazılır.

Test için gözlemlenebilir değişmezler (tamamlanmış bir kampanyadan sonra, başka kampanya yokken): `Σ_reason scc_deliveries_failed_total{channel=c}` + `scc_deliveries_sent_total{channel=c}` = o kanaldaki teslimat sayısı; `scc_deliveries_queued{channel=c}` = 0 ve `scc_deliveries_in_flight{channel=c}` = 0. (Gönderimden hemen sonraki `queued` değeri worker'a bağlı olduğundan sabit değer olarak test edilmez.)

## 2. Worker süreci — `GET /metrics` (`cmd/worker`, port 8081, yalnız iç ağ)

| Ad | Tip | Etiketler | Ne zaman |
|---|---|---|---|
| `scc_provider_requests_total` | counter | `channel`, `code` (`2xx`,`408`,`429`,`4xx`,`5xx`,`timeout`,`connection_error`) | Her provider isteği bittiğinde +1 (`WorkerMetrics.ObserveProviderResponse`) |
| `scc_provider_request_duration_seconds` | histogram | `channel` | Provider istek süresi |
| `scc_dispatcher_leader` | gauge | `channel` | Bu süreç kanalın advisory lock'unu tutuyorsa 1, değilse 0 (`delivery.Recorder.SetLeader`; `Dispatcher.Leader()` ile aynı). Yalnız lider provider'ı çağırır (README §7.1) |
| `scc_rate_limiter_wait_seconds` | histogram | `channel` | `RateLimiter.Wait` bekleme süresi (`WorkerMetrics.ObserveLimiterWait`) |

## 3. Health (REQ-TRK-03)

Hem `cmd/api` (8080) hem `cmd/worker` (8081) aynı handler'ları sunar; `cmd/mockprovider` yalnız `/healthz`.

| Uç nokta | Yanıt |
|---|---|
| `GET /healthz` | Süreç ayaktaysa her zaman `200 {"status":"ok"}`. DB'ye bakmaz |
| `GET /readyz` | DB `Ping` (1 sn zaman aşımı) başarılı → `200 {"status":"ready","checks":{"database":"ok"}}`; başarısız → `503 {"status":"not_ready","checks":{"database":"unavailable"}}`. LLM ve mock provider kontrol edilmez |

Health ve metrics uç noktaları kimlik doğrulama istemez ve `/api` önekinin dışındadır. Gövde hata metni içermez (DB hata mesajı sızmaz).

# API dokümanı

API'yi okumak ve denemek için tarayıcıdan bir sayfa açmanız yeterli. Bu sayfa Swagger UI'dır: her uç noktayı, göndereceğiniz alanları ve dönebilecek cevapları listeler, "Try it out" düğmesiyle gerçek API'yi çağırır.

## Nasıl açılır

```sh
docker compose up
```

Sonra tarayıcıda şu adreslerden birini açın:

| Adres | Ne |
|---|---|
| <http://127.0.0.1:8081/docs> | Etkileşimli doküman (web portu, `WEB_PORT`) |
| <http://127.0.0.1:8081/openapi.yaml> | Ham sözleşme dosyası |
| <http://127.0.0.1:8080/docs> | Aynı sayfa, doğrudan API portundan (`API_PORT`) |

Portlar doluysa `WEB_PORT` ve `API_PORT`'u değiştirin (ör. `WEB_PORT=8082 docker compose up`); gerçek portu `docker compose ps` gösterir. Her ikisi de yalnız `127.0.0.1`'e bağlanır, ağdan erişilmez.

Dene: sayfada `system` altındaki `GET /healthz`'i açın, **Try it out**, sonra **Execute**. `{"status":"ok"}` dönmelidir. Sayfa kendi adresine istek attığı için ayrıca bir ayar gerekmez.

Bilmeniz gereken iki şey:

- Mesaj üretimi, onay ve gönderim uç noktaları (P4 ve P5 fazları) sözleşmede yazılıdır ama henüz kodlanmamıştır. Sayfada görünürler, denerseniz `404 not_found` alırsınız. Hangileri hazır olduğunu `Routes()` listesi ve README'deki durum tablosu gösterir.
- Kimlik doğrulama yoktur (yerel demo); sayfada kilit veya token alanı bulunmaz.

## Beş adımlı akışın uç noktaları

Pazarlamacının yolu şöyledir. Her satır sözleşmedeki bir işleme karşılık gelir; ayrıntı için sayfaya bakın.

| Adım | İstek | Ne yapar |
|---|---|---|
| 1. Brief | `POST /api/campaigns` | Brief'ten kampanya açar |
| 2. Kitle | `POST /api/campaigns/{campaign_id}/segment` | Claude'a segment kuralı ürettirir, kaç kişi eşleştiğini döner |
| | `POST /api/segments/count` | Elle yazılmış bir kuralın kişi sayısını verir |
| 3. Mesajlar | `POST /api/campaigns/{campaign_id}/content` | SMS ve email metnini SSE akışıyla üretir |
| | `POST /api/campaigns/{campaign_id}/content-versions` | Metni elle düzenler (yeni sürüm olur) |
| 4. Onay | `GET /api/campaigns/{campaign_id}/preview` | Bir kişi için doldurulmuş önizleme |
| | `POST /api/campaigns/{campaign_id}/approve` | Seçilen sürümü onaylar |
| 5. Gönderim | `POST /api/campaigns/{campaign_id}/send` | Onaylı mesajları kuyruğa yazar |
| | `GET /api/campaigns/{campaign_id}/status` | Anlık sayılar |
| | `GET /api/campaigns/{campaign_id}/events` | Aynı sayılar canlı akışla (SSE) |
| | `GET /api/campaigns/{campaign_id}/deliveries` | Tek tek teslimatlar |

Sistem uç noktaları: `GET /healthz` (süreç ayakta mı), `GET /readyz` (veritabanı hazır mı), `GET /metrics` (Prometheus), `GET /api/config` (limitler, izinli değişkenler, hangi LLM'in çalıştığı).

## Hata nasıl görünür

Her hata aynı biçimdedir:

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

- `code` makinenin okuduğu kısa addır (`campaign_not_found`, `invalid_state`, `llm_unavailable`, ...). Tam liste ve anlamları: [`contracts/README.md` §5](contracts/README.md).
- `message` insanlar içindir; sır, SQL veya veritabanı hata metni içermez.
- `fields` yalnız doğrulama hatalarında bulunur ve neyin neden reddedildiğini alan alan söyler.
- `request_id` her yanıtta `X-Request-ID` başlığıyla da gelir; bir sorunu aratırken bunu kullanın.

## Doküman kodla nasıl aynı kalır

Doküman koddan üretilmez; tersine, **elle yazılan sözleşme (`docs/contracts/openapi.yaml`) esastır** ve kod ona uymak zorundadır. Sapmayı CI yakalar (gerekçe: [`DECISIONS.md` D30](../DECISIONS.md)):

| Kapı (`make ...`) | Yakaladığı |
|---|---|
| `api-lint` | Sözleşme geçerli OpenAPI değil veya bir `$ref` kırık |
| `api-routes` | Kodda olup sözleşmede olmayan ya da sözleşmede olup kodda olmayan uç nokta |
| `api-breaking` | Sözleşmede istemciyi bozan değişiklik (alan silme, tip daraltma, ...) |
| `api-types-check` | Arayüzün TypeScript tipleri sözleşmeden üretilmiş dosyadan farklı |
| `check-contract-copies` | API'ye gömülü sözleşme kopyası `docs/contracts/` ile aynı değil |
| E2E (`OPENAPI_VALIDATE=strict`) | Gerçek bir istek veya cevap sözleşmeye uymuyor |

`/docs` sayfasının gösterdiği dosya, API'ye gömülü kopyadır (`GET /openapi.yaml`); `check-contract-copies` onun `docs/contracts/openapi.yaml` ile bayt bayt aynı olduğunu garanti eder. Yani gördüğünüz doküman ile gerçek sözleşme arasında ikinci bir kopya yoktur.

Bir uç noktayı değiştirirken sıra: önce `docs/contracts/openapi.yaml`'ı düzenleyin, sonra kopyalayın (`cp docs/contracts/openapi.yaml internal/httpapi/openapi/openapi.yaml`), `make api-types` ile TypeScript tiplerini yenileyin, kodu yazın ve `httpapi.Routes()` tablosuna satır ekleyin.

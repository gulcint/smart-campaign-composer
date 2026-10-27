# SSE olayları

Kapsam: REQ-CNT-04, REQ-AI-01 (içerik), REQ-APR-01, REQ-TRK-01.
Taşıma: Server-Sent Events (`text/event-stream; charset=utf-8`), PLAN §0.5.

## 0. Ortak çerçeve kuralları

- Her olay: [`id: <n>\n`] + `event: <ad>\n` + `data: <tek satır JSON>\n` + boş satır. `data` her zaman **tek satırlık** kompakt JSON nesnesidir (çok satırlı `data:` yok). `id:` satırı **yalnız canlı durum akışında** (§2) bulunur; içerik akışında yoktur.
- Canlı durum akışı (§2) başında bir kez `retry: 3000\n\n` bloğu gönderilir (EventSource yeniden bağlanma süresi); içerik akışında `retry:` yoktur.
- Yorum satırı (`: keepalive\n\n`) **15 sn**'de bir gönderilir (heartbeat, `httpapi.Deps.Clock` + `HeartbeatInterval` ile). İstemciler yorum satırlarını yok sayar; testler olay sayarken yorumları saymaz.
- Yanıt başlıkları: `Content-Type: text/event-stream; charset=utf-8`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`.
- Stream açılmadan önce tespit edilen hatalar (404, 409, 422, 502 `llm_unavailable`) **SSE değil**, normal JSON hata gövdesiyle ve ilgili HTTP koduyla döner ([README §5](README.md#5-hata-gövdesi-ve-kodlar)). HTTP 200 + SSE başladıktan sonra hatalar yalnızca olaylarla bildirilir.
- JSON alan adları snake_case; zamanlar RFC 3339 UTC.
- `data` kompakt JSON olduğundan ham `\n`/`\r` içeremez (metindeki satır sonları JSON'da `\n` olarak kaçışlıdır); bu yüzden her olay tam bir `data:` satırıdır ve istemci ayrıştırıcısının çok satırlı `data:` birleştirmesine ihtiyaç duyulmaz.
- **Makine okunur şemalar:** her olayın `data` JSON'u `openapi.yaml` bileşenine karşı doğrulanır: `delta` → `ContentDeltaEvent`, `invalid` → `ContentInvalidEvent`, `validated` → `ContentValidatedEvent`; durum akışında üç olay da `CampaignStatus`. Eşleme `x-sse-events.schemas`'tadır. Düzyazı ile şema çelişirse şema (openapi) kazanır.

## 1. İçerik üretimi akışı

`POST /api/campaigns/{campaign_id}/content` (istek gövdesi boş veya `{}`; `Accept: text/event-stream`). Fetch/ReadableStream ile tüketilir (EventSource POST yapamaz).

### 1.1 Olaylar

| `event` | `data` şeması | Ne zaman |
|---|---|---|
| `delta` | `{"attempt": 1, "channel": "sms", "field": "body", "text": "Merhaba {{first_"}` | Modelden metin parçası geldikçe |
| `invalid` | `{"attempt": 1, "final": false, "errors": [FieldError...], "content_version_id": null}` | Bir denemenin çıktısı doğrulamayı geçemediğinde |
| `validated` | `{"attempt": 1, "content_version_id": "uuid", "version_number": 3, "content": Content, "worst_case": {"sms_runes": 160, "subject_runes": 42}}` | Çıktı geçerli ve `validated` sürüm olarak kaydedildiğinde |

Alanlar:

- `attempt`: 1 veya 2 (2 = düzeltme denemesi).
- `channel` ∈ `sms` | `email`; `field` ∈ `body` | `subject`. Geçerli çiftler: (`sms`,`body`), (`email`,`subject`), (`email`,`body`).
- `text`: boş olmayan parça (boyutu sözleşme değildir: gerçek istemcide API'nin `partial_json` parçalarına bağlıdır, sahte LLM'de ≤ 20 karakter); aynı `attempt` içinde aynı (`channel`,`field`) için tüm `text`'lerin sırayla birleşimi o alanın modelin ürettiği değeridir.
- `final`: `true` ise bu olay akışın **son** olayıdır.
- Akış sürerken (kampanyanın üretim kilidi tutulurken) aynı kampanyaya segment/içerik üretimi, içerik düzenleme, onay ve gönderim istekleri **409 `generation_in_progress`** alır; önceki `validated` sürüm de bu sırada onaylanamaz. Kilit yalnızca yazma işlemlerini engeller: `GET` uç noktaları (kampanya, sürümler, önizleme, durum) akış sürerken normal yanıt verir.
- `errors`: en az bir `FieldError` (`{"path","code","message"}` + uzunluk kodlarında `limit`, `actual`); şema README §5'te.
- `content_version_id` (`invalid` içinde): `final: false` iken **her zaman `null`**. `final: true` iken: son denemenin çıktısı şemaya uygun ama içerik kurallarını geçemediyse (`sms_too_long`, `subject_too_long`, `email_body_too_long`, `unknown_variable`, `malformed_variable`, `required`) bu taslak `status: "invalid"` bir içerik sürümü olarak saklanır ve kimliği burada döner. `required` yalnız anahtar **var ama değeri boş** ise saklanabilir sayılır; **eksik anahtar** `schema_violation`'dır ve saklanmaz. Aksi halde (`invalid_json`, `schema_violation`, `truncated_output`, `no_tool_call`, `llm_unavailable`, `internal_error`) `null`.
- `content` (`validated` içinde): `{"sms":{"body":...},"email":{"subject":...,"body":...}}` — kaydedilen sürümün şablon metni.
- `worst_case` (`validated` içinde): güncel segmentin en uzun değerleriyle doldurulmuş SMS ve konu uzunluğu (`content.WorstCase`); `sms_runes ≤ 160`, `subject_runes ≤ 80` garantilidir. UI sayaçtaki ikinci satırı bundan gösterir.

### 1.2 Sıralama kuralları (normatif)

```text
stream    := attempt1 ( terminal | invalid(final=false) attempt2 terminal )
attempt_n := delta*                      // attempt alanı n
terminal  := validated | invalid(final=true)
```

1. Akış **tam olarak bir** terminal olayla biter: `validated` veya `invalid` (`final: true`). Terminal olaydan sonra sunucu bağlantıyı kapatır; ardından başka olay gelmez.
2. `invalid` (`final: false`) yalnızca deneme 1'in sonunda ve en fazla bir kez gelir. Ondan sonraki ilk `delta` `attempt: 2` taşır. **İstemci bu noktada önceki taslağı atar** ve alanları baştan doldurur ("delta'lar baştan başlar").
3. Deneme 2'de de doğrulama başarısızsa terminal olay `invalid` (`final: true`, `attempt: 2`) olur. Deneme 1'de doğrudan `final: true` gelmesi yalnız `llm_unavailable` (zaman aşımı dahil) ve `internal_error` için mümkündür (düzeltme denemesi yapılmaz).
4. Bir denemede sıfır `delta` olabilir (ör. ilk bayt gelmeden biten bozuk JSON).
5. `delta` olayları alan sırasını korur ama sunucu farklı alanlar arasında geçiş yapabilir; istemci (`channel`,`field`) çiftine göre biriktirir.
6. Kampanya durumu: `validated` olayı gönderilmeden **önce** sürüm kaydedilmiş ve kampanya `content_ready` olmuştur (önceki durum `approved` ise onay düşmüştür). Son `invalid` **hiçbir şeyi değiştirmez** (VARSAYIM): durum aynı kalır (`audience_ready`, `content_ready` veya `approved`), etkin onay düşmez ve saklanan `invalid` taslak "en son sürüm" (`Campaign.latest_content_version` = güncel segment revizyonunun en yüksek numaralı **validated** sürümü) olmaz. Başarısız bir yeniden üretim, onaylı bir kampanyayı bozamaz.

### 1.3 Yarıda kesilme

| Durum | Davranış |
|---|---|
| **Model stream'i** erken bitti (`stop_reason=max_tokens`, JSON kapanmadı, sahte LLM `truncated_stream`) | Bu bir geçersiz çıktıdır: `invalid` (`errors: [{"path":"","code":"truncated_output",...}]`). Deneme 1 ise `final:false` + deneme 2; deneme 2 ise `final:true`, `content_version_id: null`. |
| Model yanıtı `tool_use` bloğu **olmadan** bitti (yalnız metin) | Geçersiz çıktı: `invalid` (`errors: [{"path":"","code":"no_tool_call"}]`); deneme 1 ise `final:false` + deneme 2; deneme 2 ise `final:true`, `content_version_id: null`. |
| LLM erişimi HTTP 200'den sonra koptu (`llm.ErrUnavailable`, deneme 1 veya 2) | Düzeltme denemesi **yok**; terminal `invalid` (`final: true`, `errors: [{"path":"","code":"llm_unavailable"}]`, `content_version_id: null`); durum değişmez. |
| Üretim `campaign.MaxGenerationDuration` (**120 sn**, VARSAYIM) içinde bitmedi | LLM çağrısı iptal edilir; terminal `invalid` (`llm_unavailable`), kilit bırakılır. |
| **İstemci bağlantısı** terminal olaydan önce koptu (sahte LLM `stalled_stream` ile test edilir) | Sunucu `ctx` iptalini görür, LLM çağrısını iptal eder, doğrulama/kayıt aşamasına gelmediyse **hiçbir içerik sürümü saklamaz**, kampanya durumu değişmez ve **üretim kilidi bırakılır** (sonraki üretim/düzenleme/onay/gönderim isteği 409 almaz). Kesilen içerik onaylanamaz (onaylanacak yeni `validated` sürüm yoktur). **İnce sınır:** kayıt işlemi (tek transaction) başladıysa istemci o sırada koparsa bile tamamlanır; istemcinin hiç görmediği bir `validated` sürüm bu durumda `GET /api/campaigns/{id}` ile görünür. Testler yalnız `stalled_stream`'in kapsadığı durumu (doğrulama öncesi kopma → sürüm yok) iddia eder. |
| Sunucu iç hatası stream sırasında | Terminal `invalid` (`final: true`), `errors: [{"path":"","code":"internal_error"}]`, `content_version_id: null`. |

Sunucu terminal olayı yazar yazmaz bağlantıyı kapatır (yanıt gövdesi biter); terminal olay gönderilmeden gövdenin bitmesi, istemci için "yarıda kesildi"dir.

### 1.3.1 İstemci kuralları (normatif; ui.md §5.2 bunları uygular)

1. Bilinmeyen `event` adları sessizce yok sayılır (ileriye uyumluluk); yorum satırları yok sayılır.
2. `data` JSON olarak ayrıştırılamıyor veya ilgili şemaya uymuyorsa akış **kesilmiş** sayılır (aşağıdaki "terminal olay yok" davranışı); istemci bunu `ApiError` olarak göstermez.
3. Terminal olaydan (`validated` veya `invalid` `final: true`) sonra gelen her şey yok sayılır.
4. Terminal olay olmadan gövde biterse veya ağ hatası olursa istemci kendiliğinden **yeniden bağlanmaz** (yeniden üretim kullanıcı kararıdır; her üretim yeni bir LLM maliyeti ve yeni sürüm demektir).
5. Aynı `attempt` içinde (`channel`,`field`) çiftine göre biriktirilen metin taslaktır; `validated`'daki `content` otoritedir ve alan metinlerini yeniden yazar.

### 1.3.2 Neyin ne zaman saklandığı (özet tablo)

| Sonuç | `content_versions` | Kampanya durumu | Etkin onay | `latest_content_version` |
|---|---|---|---|---|
| `validated` | yeni satır, `validated`, `source=llm`, `llm_attempts` 1 veya 2 | `content_ready` | düşer (`content_changed`) | yeni sürüm |
| son `invalid`, `content_version_id` dolu | yeni satır, `invalid`, `source=llm`, `validation_errors` dolu | değişmez | değişmez | değişmez |
| son `invalid`, `content_version_id: null` | satır yok | değişmez | değişmez | değişmez |
| `final: false` `invalid` (deneme 1 sonu) | satır yok | değişmez | değişmez | değişmez |
| bağlantı kopması / `delta`'lar sırasında iptal | satır yok | değişmez | değişmez | değişmez |

Sürüm yalnız `validated` olayından **önce** ve son `invalid` olayından **önce** (kimlik olayda döndüğü için) kaydedilir; ara (`delta`, `final: false`) olaylar hiçbir şey kaydetmez.

### 1.4 Örnekler

`ok` senaryosu (kısaltılmış):

```text
event: delta
data: {"attempt":1,"channel":"sms","field":"body","text":"Merhaba {{first_name"}

event: delta
data: {"attempt":1,"channel":"sms","field":"body","text":"}}, sepetindeki ürünl"}

... (diğer delta'lar: email.subject, email.body)

event: validated
data: {"attempt":1,"content_version_id":"0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11","version_number":1,"content":{"sms":{"body":"Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"},"email":{"subject":"Sepetin seni bekliyor: %30 indirim fırsatı","body":"Merhaba {{first_name}},\n\n..."}},"worst_case":{"sms_runes":160,"subject_runes":42}}
```

`then_ok_after_repair`:

```text
event: delta
data: {"attempt":1,"channel":"sms","field":"body","text":"İndirim İndirim İndi"}
... 
event: invalid
data: {"attempt":1,"final":false,"errors":[{"path":"sms.body","code":"sms_too_long","message":"SMS body exceeds 160 characters","limit":160,"actual":161}],"content_version_id":null}

event: delta
data: {"attempt":2,"channel":"sms","field":"body","text":"Merhaba {{first_name"}
...
event: validated
data: {"attempt":2,"content_version_id":"...","version_number":1,"content":{...},"worst_case":{"sms_runes":160,"subject_runes":42}}
```

`over_limit_sms` (son olay):

```text
event: invalid
data: {"attempt":2,"final":true,"errors":[{"path":"sms.body","code":"sms_too_long","message":"SMS body exceeds 160 characters","limit":160,"actual":161}],"content_version_id":"5c1d..."}
```

## 2. Canlı gönderim durumu akışı

`GET /api/campaigns/{campaign_id}/events` — EventSource uyumlu (GET, özel başlık gerekmez, kimlik doğrulama yok).

### 2.1 Olaylar

| `event` | `data` | Ne zaman |
|---|---|---|
| `snapshot` | `CampaignStatus` | Bağlantı açılınca **ilk** olay, her zaman tam olarak bir kez |
| `status` | `CampaignStatus` | Sunucu `Service.Status`'u enjekte saatle **1 sn**'de bir yoklar (`httpapi.Deps.StatusPollInterval`); sonuç son gönderilenden (`updated_at` hariç) farklıysa gönderilir, değilse gönderilmez. Yani en fazla saniyede bir `status` (VARSAYIM) |
| `completed` | `CampaignStatus` (`state: "completed"`) | Kampanya `completed` olunca **son** olay; ardından sunucu bağlantıyı kapatır |

Akışın başında bir kez `retry: 3000` satırı gönderilir. Her olay `id: <n>` taşır; `n` bağlantı başına 1'den başlayıp artar. `Last-Event-ID` desteklenmez: yeniden bağlanan istemci yeni bir `snapshot` alır.

`CampaignStatus` (openapi `CampaignStatus` şemasıyla aynı; `GET /api/campaigns/{id}/status` yanıtı da budur):

```json
{
  "campaign_id": "uuid",
  "state": "sending",
  "counts":     { "total": 2410, "queued": 1800, "sending": 10, "sent": 590, "failed": 10 },
  "by_channel": {
    "sms":   { "total": 1200, "queued": 900, "sending": 5, "sent": 290, "failed": 5 },
    "email": { "total": 1210, "queued": 900, "sending": 5, "sent": 300, "failed": 5 }
  },
  "sending_started_at": "2026-01-15T12:00:05Z",
  "completed_at": null,
  "updated_at": "2026-01-15T12:00:11Z"
}
```

Değişmezler: `total = queued + sending + sent + failed` (her kanal ve toplam için); `counts.x = by_channel.sms.x + by_channel.email.x`. Teslimat yoksa (`draft`…`approved`) tüm sayaçlar 0.

### 2.2 Sıralama kuralları

1. İlk olay `snapshot`.
2. Kampanya bağlantı anında zaten `completed` ise: `snapshot` ardından hemen `completed`, sonra kapanış.
3. `completed` olayında `queued = sending = 0` ve `sent + failed = total`. Kalıcı başarısız teslimat olsa bile kampanya `completed` olur ve `failed > 0` görünür (REQ-TRK-01).
4. Sayaçlar ardışık olaylarda geri gitmez: `sent` ve `failed` monoton artar.
5. Bilinmeyen kampanya → stream açılmaz, HTTP 404 JSON.

### 2.3 Örnek

```text
retry: 3000

id: 1
event: snapshot
data: {"campaign_id":"...","state":"sending","counts":{"total":4,"queued":4,"sending":0,"sent":0,"failed":0},"by_channel":{...},"sending_started_at":"...","completed_at":null,"updated_at":"..."}

id: 2
event: status
data: {"campaign_id":"...","state":"sending","counts":{"total":4,"queued":1,"sending":1,"sent":2,"failed":0},...}

: keepalive

id: 3
event: completed
data: {"campaign_id":"...","state":"completed","counts":{"total":4,"queued":0,"sending":0,"sent":3,"failed":1},...,"completed_at":"..."}
```

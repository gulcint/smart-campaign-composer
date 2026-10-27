# DECISIONS

Önemli kararlar ve nedenleri. Her faz kapısında güncellenir (`docs/PLAN.md` §3.2). Her karar: **bağlam → karar → neden → alternatifler/bedel**. "Doküman" case dokümanıdır; dokümanda olmayan her şey VARSAYIM olarak işaretlidir. Ayrıntılı gereksinimler `docs/requirements.md`, ad sözleşmeleri `docs/contracts/`.

Durum: **P0 ve P1 tamamlandı** (`p0-done`, `contracts-v1`). Kod yok; aşağıdakiler tasarım kararlarıdır. P2'den itibaren uygulama kararları eklenir. P4 öncesi içerik/onay sözleşmeleri sertleştirildi (D37–D48; D40 sahip kararıyla iyimser eşzamanlılığa çevrildi).

---

## 1. Mimari

### D1 — Kuyruk PostgreSQL'dedir: `deliveries` tablosu kuyruğun kendisi
- **Bağlam:** Doküman kuyruk/veritabanı seçimini bırakıyor; zorunlu olanlar: asenkron kuyruk, kanal başına ≤100/sn, aynı mesaj iki kez gitmez, retry, canlı durum.
- **Karar:** Ayrı bir kuyruk servisi yok. Teslimat kayıtları `UNIQUE(campaign_id, channel, customer_id)` kısıtlı tek tabloda; worker'lar `FOR UPDATE SKIP LOCKED` ile iş alır.
- **Neden:** "İki kez gitmez" garantisini veritabanı kısıtı verir; Redis'te tekillik ayrıca kurulup durum yine Postgres'te tutulurdu (iki sisteme yazma, tutarsızlık). Gönderim isteği, onaylanan içerik sürümü için tüm teslimat kayıtlarını tek işlemde oluşturur (ya hepsi ya hiçbiri); onaydan sonra düzenleme yapılırsa henüz kayıt olmadığı için artık kayıt kalmaz. Retry/canlı durum/metrics aynı tablodan okunur. `docker compose up` ve testler tek bağımlılıkla çalışır. Bonus (zamanlanmış gönderim) neredeyse bedava.
- **Bedel:** Gerçek Insider ölçeğinde (milyonlar) Redis Streams/NATS/Kafka gerekir; `Queue` arayüzü bu geçişi mümkün kılar. Dağıtık hız sınırı Redis'te daha doğaldı; burada kanal başına tek dispatcher ile çözüldü (D5).

### D2 — Kendi küçük kuyruğumuz; hazır kütüphane yok
`deliveries` tablosu zaten gerekli (tekillik, durum, retry); review görüşmesinde her satırı savunulabilir kod istendi. River gibi kütüphane tekillik/durum modelini yine bizden isteyecekti.

### D3 — İdempotency iki katmanlı
Veritabanı kısıtı (aynı kişiye aynı mesaj için ikinci kayıt oluşamaz) + sağlayıcıya her istekte aynı `Idempotency-Key` (= teslimat kimliği). Provider "kabul etti ama yanıt kayboldu / worker durumu yazamadan düştü" senaryosunda retry'ı tekilleştirir. Tekilleştirme **müşteri bazındadır**; aynı telefonu/e-postayı paylaşan iki ayrı kayıt iki teslimat alır (VARSAYIM; adres bazlı tekilleştirme kapsam dışı).

### D4 — Kampanya durum makinesi, kampanya düzeyinde `failed` yok
`draft → audience_ready → content_ready → approved → sending → completed`. Teslimat hataları `failed` **teslimat** olarak sayılır; kampanya yine `completed` olur ve başarısız sayısı görünür. Neden: "kampanya başarısız" tek bir testlenebilir koşula bağlanamıyordu. Onaylı kampanyada düzenleme onayı düşürür (`content_ready`), `sending`/`completed`'da düzenleme 409.

### D5 — Hız sınırı: token bucket, burst = 1, kanal başına tek dispatcher
- **Tanım (VARSAYIM):** her yarı-açık 1 sn'lik pencerede kanal başına ≤100 istek başlangıcı; ardışık başlangıçlar arası ≥10 ms.
- **Neden burst = 1:** Burst'ü 100 olan bir kova kayan pencerede ~199 isteğe izin verirdi.
- **Çok worker:** Kanal başına bir dispatcher (`pg_try_advisory_lock`) çalışır; worker sayısı sınırı değiştirmez, retry'lar da sınıra dahil. Sınır, sahte saatli birim testle kesin; E2E'de ağ gecikmesi payıyla (tolerans) ölçülür.

### D6 — Retry stratejisi (VARSAYIM; doküman tasarımı bize bırakıyor)
5 deneme (ilk deneme dahil); 5xx/408/zaman aşımı → üstel backoff, taban 1 sn, tavan 60 sn, tam jitter; diğer 4xx → retry yok; **429** `Retry-After`'e uyar ve hata bütçesinden değil **ayrı bütçeden** sayılır (en fazla 20 adet veya ilk denemeden itibaren 10 dk). Neden ayrı: mock provider bilerek 429 döndürüyor; aynı bütçede olsaydı kasıtlı 429'lar toplu `failed` üretirdi. Jitter tek bir tohumlu çekilişle deterministik, testler kesin süreleri hesaplayabilir.

---

## 2. LLM entegrasyonu (değerlendirmenin %25'i)

### D7 — Yapılandırılmış çıktı: araç kullanımı, `tool_choice: auto` + `strict`, tek düzeltme denemesi
- **Bağlam:** Doküman JSON çıktı ve backend doğrulaması istiyor.
- **Doğrulanan gerçekler (resmî doküman):** `claude-opus-5-5` ve `claude-sonnet-5-5` zorunlu `tool_choice`'u HTTP 400 ile reddeder (Haiku 5.5 kabul eder); strict modda `minLength`/`maxItems`/`oneOf` desteklenmez; `temperature`/`top_p`/`top_k` gönderilmez.
- **Karar:** Hem segment hem içerik için araç kullanımı. Araç çağrılmazsa ayrı hata (`no_tool_call`). Çıktı **her zaman** backend'de yeniden doğrulanır; başarısızsa hatalar modele geri verilip **bir kez** yeniden üretilir; yine başarısızsa segmentte 502 `llm_invalid_output`, içerik akışında son-işaretli `invalid`; kampanya durumu değişmez.
- **İki katmanlı şema:** Backend'in doğruladığı **tam yapısal şema** ile modele gönderilen **strict uyumlu varyant** ayrı dosyalardır; uzunluk/adet/derinlik kuralları yalnız backend doğrulayıcıdadır. Varyant ile tam şema birbirini tutmak zorunda (birim test şartı).
- **Bedel / açık risk:** Strict modun birleşik tip sayacı bizim segment şemamızda 16 sınırını aşabilir; yalnızca gerçek API çağrısı doğrular. Aşarsa o araç `strict: false` gönderilir, backend doğrulaması aynı kalır (`docs/contracts/README.md` §10).
- **Alternatif:** `output_config` yapılandırılmış çıktı: alan bazlı delta vermiyor, `tool_choice` ile etkileşimi belgelenmemiş; seçilmedi.

### D8 — LLM çıktısı asla SQL olmaz
Segment kuralı kapalı bir DSL'dir (izinli alan/operatör, derinlik ≤3, koşul ≤10); backend parametreli sorgu kurar. Model bir `sql`/`where` alanı döndürürse şema ihlalidir; değer içindeki SQL parçası yalnızca değerdir. Tam şema yalnız **yapıyı** denetler, alan/operatör adları `segment.Validate`'te `unknown_field`/`unknown_operator` olur (ilk taslakta şema adları sıralıyordu ve aynı kural iki yerde farklı uygulanıyordu; düzeltildi).

### D9 — İzin (consent) sistem tarafından, kanal bazında uygulanır; modelin işi değildir
Model yalnızca kitle koşulunu yazar. SMS/e-posta izni ve iletişim bilgisi (telefon/e-posta) sistem tarafından kanal başına filtrelenir; sayılar kanal bazında ve izin filtresinden sonra gösterilir. Gönderim anında izin/iletişim tekrar okunur: kalktıysa sağlayıcıya çağrı yapılmadan `failed (recipient_unavailable)`.

### D10 — Karakter limitleri doldurma **sonrası** ölçülür; rune sayımı
SMS ≤160, konu ≤80 **Unicode rune**; `ş ğ İ` tek karakter. Üretim ve düzenleme aynı doğrulayıcıdan geçer; her değişken kitledeki en uzun değeriyle (boş `first_name` için yedek metin dahil) doldurulur — bilerek **muhafazakâr üst sınır**. Gönderimde alıcı bazında ikinci denetim yapılır. **Bilinen sınır:** gerçek SMS segmentleri GSM-7/UCS-2'ye göre değişir (Türkçe karakterli mesaj 70'e düşer); case "160 karakter" dediği için karakter sayımı kullanıldı, arayüz bilgi olarak gösterir.

### D11 — Anahtar yoksa sahte LLM: `LLM_PROVIDER=auto|real|mock`
`auto` (varsayılan) key varsa gerçek Claude, yoksa sahte LLM; `real` ve key yoksa açılışta hata. Neden: değerlendirici `.env`'ye key koymayabilir; doküman "geliştirme sırasında mock LLM" diyor. Arayüzde "Mock LLM" rozeti. Sahte LLM deterministik ve senaryo seçilebilir (`X-Mock-LLM-Scenario` başlığı, **yalnızca etkin sağlayıcı mock iken** dikkate alınır). Gerçek istemci test ikilisinde kurulamaz (`ErrRealClientForbidden`).

### D12 — Canlı akış: SSE
İçerik üretimi **POST + fetch tabanlı akış** (EventSource GET-only); canlı durum GET + EventSource, otomatik yeniden bağlanma. Neden SSE: veri tek yönlü (sunucu → tarayıcı), düz HTTP, WebSocket gerekmez. Stream sırasında görünen metin **taslaktır**; `validated` olayı gelmeden onay mümkün değildir. Bir üretim sürerken onay/düzenleme 409 `generation_in_progress`; kilit istemci kopunca ya da en geç 120 sn sonra açılır (VARSAYIM).

---

## 3. Süreç, roller ve güvenlik

### D13 — Test yazan, kodu yazan ve review eden ayrı; test yazarı koda bakmaz
Testler yalnızca doküman, gereksinim kataloğu ve sözleşmelerden yazılır ki kod mimarisinin istenenleri karşılayıp karşılamadığı ölçülebilsin. Uygulama katmanları: koda bakmayan **sparse worktree**, yalnızca bu ajanda çalışan **PreToolUse koruması**, CI'daki **rol yolu kontrolü** ve reviewer. **Dürüst sınır:** bir ajanın okumasını her ortamda %100 engellemek mümkün değil; katmanlar birlikte sınırı teknik olarak destekler.
- **Tek istisna:** `testdata/seed/manifest.json` seed kodu tarafından üretilir; kodu yazan commit'ler, test yazarı yalnızca okur (determinizm testi baytları yeniden üretir).

### D14 — Depo: önce GitLab denendi, GitHub (public) seçildi
GitLab.com paylaşımlı runner'ları hesap kimlik doğrulaması istedi; doğrulama yapılamadı, ilk pipeline hiç job çalıştırmadan düştü. Proje silindi. GitHub'da repo public: hassas veri repoda tutulmadığı için kabul edildi; ücretsiz planda `main` ruleset'i çalışır (PR + `ci-ok` zorunlu, force push yok, bypass yok). Commit e-postası GitHub noreply adresidir.
- **Ders:** GitLab'da ilk push `main` değil bir dal olunca o dal varsayılan ve korumalı oldu; `main` önce push edilir.

### D15 — Merge yetkisi: `ci-ok` yeşilse ajan merge eder (sahip kararı, 2026-10-26)
Koruma hook'u `gh pr merge`'e yalnızca açık PR numarası + `--squash` ile ve o PR'da `ci-ok` yeşilken izin verir; `--admin` yasak, durum okunamazsa engeller. Sunucu tarafında aynı kuralı ruleset zorlar. Yerel hook'lar `--no-verify` ile atlanabilir; **asıl zorlayıcı CI + ruleset**'tir.

### D16 — Güvenlik
- Anahtar yalnızca `.env`'de; repoda boş `.env.example`; `.dockerignore` sırları her derinlikte dışlar; sır taraması commit/push/CI'da (gitleaks); CI'da anahtar tanımlı değildir, testler mock ile koşar.
- Kişisel veri: seed yalnızca `example.com` e-postaları ve kurgusal telefon bloğu; TC kimlik/doğum tarihi/adres/ödeme alanı yok (veri minimizasyonu). Loglarda müşteri kimliği geçer, e-posta/telefon geçmez.
- LLM/düzenleme metni arayüzde asla HTML olarak yorumlanmaz (`v-html` yasak, e-posta önizlemesi `sandbox` çerçevede).
- Uygulamanın DB rolü DDL çalıştıramaz; migration ayrı rolle koşar (`cmd/migrate`).
- Kimlik doğrulama yok (VARSAYIM): yerel demo; `/metrics` iç ağdan.
- Repo public olduğundan `ai-sessions/` yayımlanmadan önce temizleyici + gitleaks + elle gözden geçirme zorunludur.

---

## 4. Arayüz

### D17 — Component yığını: shadcn-vue + Reka UI + Tailwind CSS v4
Component kaynağı repoya kopyalandığı için `data-testid`, ARIA ve token'lar üzerinde tam kontrol var; Tailwind v4'ün CSS token'ları görsel dille birebir eşleşir. En güçlü alternatif PrimeVue 5'ti (bu uygulamada karmaşık tablo az olduğu için seçilmedi). Grafik kütüphanesi yok; referanstaki gibi özel çubuklar.

### D18 — Metin rengi: turuncu vurgu rengi (sahip kararı)
Başlıklar, vurgular, anahtar sayılar ve linkler turuncu; uzun gövde ve LLM metni koyu. Parlak turuncu açık zeminde AA'yı geçemediği için (2.73:1) küçük turuncu metin koyu tonu (`--orange-700`) kullanır; CTA gradient'i beyaz metin için koyulaştırıldı. Turuncu miktarı tek bir ayarla değişir (`minimal` / `accent` / `bold`).

### D19 — Arayüz dili Türkçe; metinler tek i18n dosyasında (VARSAYIM)
Doküman Türkçe ve örnek brief Türkçe. İngilizce sonradan eklenebilir.

---

## 5. Veri ve gözlemlenebilirlik

### D20 — "Alışveriş yapmayan" = son 30 günde satın alma yok
"Sepete ekledikten sonra satın almamış" okuması reddedildi; seed'de iki okumanın farklı sayı verdiği bir grup (`purchase_before_cart_add`) bulunur ki seçim gerçekten test edilsin.

### D21 — Seed: 5000 müşteri, deterministik, manifestli
`--seed/--users/--ref-time` ile bayt bayt tekrarlanabilir; beklenen sayılar `manifest.json`'a yazılır, testler onlara karşı çalışır. Sınır durumları bilinçli üretilir (29/30/31 gün, satın alanlar, izinsiz İstanbul'lular, boş/çok uzun ad, paylaşılan telefon).

### D22 — Metrikler veritabanından hesaplanır
Teslimat sayaçları DB'den okunur; seed `--reset` sonrası düşebilir. Kabul edilen bedel: ayrı bir sayaç deposu kurmamak.

### D23 — Her iki LLM aracı `strict: true` ile gider
Segment aracında 16 birleşim tipi sınırı aşılabilir endişesi vardı; 2026-10-26'da gerçek API çağrısıyla (`claude-sonnet-5-5`, `tool_choice: auto`) segment ve içerik araçları strict kabul edildi ve şemaya uygun çıktı verdi. Backend doğrulaması yine her durumda çalışır. İleride 400 gelirse yedek: o aracı `strict: false` göndermek.

### D24 — Compose seed'i yıkıcı değil: `--if-empty`
`docker compose up` her açılışta seed'i yeniden koşturur. `--ref-time=now` her seferinde farklı veri kümesi üretir; `--reset` ile bu, kampanya ve teslimatları silerdi. Bunun yerine `--if-empty`: seed çalışmışsa hiçbir şeye dokunmadan çıkar. `--reset` yalnızca elle kullanılan açık bir komut (`seed.md` §1).

### D25 — Mock provider P2'ye çekildi
REQ-DLV-01 "docker compose up, mock provider dahil" der ve sözleşme provider'ı tam tarif eder. Yalnızca `/healthz` yanıtlayan bir iskelet yerine sözleşmeye uygun gerçeği yazıldı; REQ-MOCK testleri P5'te gelir (o zamana kadar bu kodun test kapsamı sınırlı, bilinen risk).

### D26 — Test koduna özel lint istisnaları
`.golangci.yml` yalnızca `_test.go` ve `test/` için gosec G101/G204/G304/G702, noctx, S1025 ve errorlint "comparing with" bulgularını susturur. Gerekçe: sahte key, test binary'sini yeniden çalıştırma ve tablo değerlerini karşılaştırma testlerin kasıtlı davranışıdır. Üretim kodu için istisna yok.

### D27 — Bilinçle ertelenen CI kontrolleri
PLAN §6'daki CodeQL (#17) ve imaj dosya sistemi / `docker history` taraması (#5) P2'de eklenmedi; P6 (sertleştirme) kapısına taşındı. Eklenenler: `db-roundtrip`, tool-şema sapma kontrolü, `compose-ports` (db ve mockprovider host portu yayınlamaz).

### D28 — `/metrics` yayınlanan API portunda, yalnız 127.0.0.1
`metrics.md` `/metrics`'i 8080'de sunar; PLAN'ın "yalnız iç ağ" ifadesi bu karara göre güncel sayılır. Kimlik doğrulama olmadığı için API portu yalnız `127.0.0.1`'e bağlanır (yerel demo).

### D29 — Compose `.env`'i isteğe bağlı okur
`api` servisi `env_file: .env` (`required: false`) kullanır. `.env` yoksa mock LLM; varsa ve key doluysa gerçek Claude. Yerelde "key'siz çalışır" kontrolü için `.env` anahtarı boşaltılmalı veya temiz klon kullanılmalıdır. `make test` etkilenmez (`compose.test.yml` `.env` okumaz).

### D30 — Sözleşme önce, kod onu izler: API/doküman sapma koruması (sahip onayı, 2026-10-27)
`docs/contracts/openapi.yaml` elle yazılan tek doğruluk kaynağı kalır. Koddan spec üretimi ve sunucu kodu üretimi reddedildi: sapmayı yeni sözleşme yapar, SSE'ye uymaz, test yazarının spec'ten test yazma kuralını bozar. Bunun yerine CI'da dört kontrol: E2E'deki her istek ve yanıtın spec'e karşı doğrulanması (`OPENAPI_VALIDATE=strict`, libopenapi-validator), router rotaları ile spec yollarının çift yönlü eşleşmesi (`api-routes`, `httpapi.Routes()`), spec lint (`api-lint`, Redocly) ve `main`'e karşı kırıcı değişiklik (`api-breaking`, oasdiff), ayrıca frontend tiplerinin spec'ten üretilmesi (`api-types-check`). Sınır: yalnızca E2E'nin çağırdığı işlemleri görür, bunu "her işlem en az bir kez çağrılmalı" kuralı azaltır; davranış anlamı (hata kodu anlamı, retry, idempotency, SSE semantiği) insan incelemesinde kalır. SSE olay şemaları P4'te gelir. Etkileşimli doküman (`/docs`, Swagger UI) başta P6'ya bırakılmıştı; sahip talebiyle öne çekildi, bkz. D36.

### D31 — Başlıkta Insider One logosu (sahip kararı, 2026-10-27)
Sahip, `assets/InsiderOneLogo.jpeg` dosyasının web arayüzünde kullanılmasına izin verdi. Dosya `web/public/brand/` altına kopyalandı; diğer iki pazarlama görseli repoya girmez. Marka adı hakları Insider One'a aittir, logo yalnızca bu vaka çalışmasının arayüzünde kullanılır.

### D32 — Onarım turu gerçek API ile doğrulandı (2026-10-27)
Strict araçla "önceki geçersiz çıktı sahte `tool_use` olarak yeniden oynatılır + `is_error` `tool_result`" biçimi, ekstra anahtarlı çıktı ve boş `{}` ile `claude-sonnet-5-5` üzerinde denendi: ikisi de HTTP 200, geçerli `tool_use` döndü. D23'teki açık soru ("onarım turu reddedilebilir mi") kapandı.

### D33 — Agent'lar için veri kaybettiren git komutları engellidir
P2'de `git worktree remove --force` commit edilmemiş test dosyalarını sildi (nesne deposundan kurtarıldı). `scripts/claude/guard-git.sh` artık `worktree remove --force`, `branch -D`, `clean -f`, `reset --hard`, `checkout -f|--`, `switch -f`, `restore` (yalnız `--staged` serbest), `stash drop|clear` komutlarını engeller; silme için `scripts/worktree-remove.sh`. Testler `make test-hooks`.

### D34 — Slice → main PR'ında `api-breaking-ok` etiketi gerekir
`main`'deki `openapi.yaml`, P1'de yazılmış ve hiç uygulanmamış bir taslaktı; P3 slice'ı onu sıkılaştırdı (`$ref`'li şemalar, kapalı nesneler, hata kodu kümeleri, 400/413/500 yanıtları). `oasdiff` bunu 13 kırıcı değişiklik sayar. Hiçbir istemci bu taslağa bağlı olmadığı için kırıcılık yoktur; bu tek PR `api-breaking-ok` etiketiyle geçer. Bundan sonra `api-breaking` PR'ın kendi taban branch'ine karşı çalışır.

### D35 — Segment ayrıştırma doğrusal zamanlıdır
Bağımsız review, 72 KB'lık iç içe `{"not":…}` gövdesinin eski `Parse`'ta 1,3 sn ve 500 MB harcadığını ölçtü (derinlik sınırı ayrıştırmadan sonra uygulanıyordu). `Parse` artık belgeyi tek geçişte okur, sınırı aşan dalı ayrıştırırken keser (~2 ms). LLM kaynaklı anahtar adları hata gövdesine yalnızca düz tanımlayıcı ve ≤64 karakter ise yansır, alan hatası sayısı 20 ile sınırlıdır. SQL gün aritmetiği saat cinsindendir ve havuz oturumları UTC'ye sabitlenir (`Match` ile DST'ye bağlı olmayan eşdeğerlik).

### D36 — Etkileşimli API dokümanı: `/docs` (Swagger UI), sahip talebiyle öne çekildi (2026-10-27)
Sahip "swagger ve API dokümantasyonu tamamlansın" dedi; D30'daki P6 ertelemesi kalktı. Kaynak yine elle yazılan `docs/contracts/openapi.yaml`; API, gömülü kopyayı (`internal/httpapi/openapi/`, `check-contract-copies`) olduğu gibi `GET /openapi.yaml` olarak sunar, ikinci bir kopya yoktur. Swagger UI (`swagger-ui-dist` 5.34.0, sürüm sabit, dosyalar değiştirilmemiş, `LICENSE` ve `NOTICE` duruyor, Apache-2.0) Go ikilisine gömülüdür ve `GET /docs`, `GET /docs/{asset}` ile gelir; CDN yok. Başlatma kodu satır içi değil, harici `swagger-initializer.js`'tir. Bu yüzden **CSP gevşetilmedi**: nginx'teki mevcut politika (`script-src 'self'`, `connect-src 'self'`, `style-src ... 'unsafe-inline'`) Swagger UI için yeterli. nginx'te `add_header` konuma özel olduğundan yeni konum aynı politikayı yineler ve API'nin kendi başlık kopyalarını `proxy_hide_header` ile gizler, böylece her başlık tek kez çıkar. API de aynı başlıkları (`nosniff`, `X-Frame-Options`, `Referrer-Policy`, CSP) doğrudan 8080'de de verir.

Üç sapma ve nedeni:
1. Spec'teki `servers` `/api` değil `/`. Yollar zaten `/api/...` önekini taşır (`/healthz`, `/readyz`, `/metrics` öneksizdir); `/api` yazmak "Try it out"u `/api/api/campaigns`'e gönderirdi. `/` göreli olduğundan istek sayfanın kendi origin'ine, yani web konteynerine gider.
2. Spec'in göreli `$ref`'leri (`schemas/*.schema.json`) `/openapi.yaml`'a göre `/schemas/...` olarak çözülür; bu yüzden iki şema dosyası da gömülü kopyadan `GET /schemas/{schema}` ile sunulur. Demetlenmiş ikinci bir spec üretmek sapma riski olurdu.
3. nginx `/healthz`, `/readyz` ve `/metrics`'i de API'ye aktarır; aksi halde spec'teki bu üç yol web origin'inde SPA'nın `index.html`'ini döndürür ve "Try it out" yanıltıcı olurdu. `/metrics` zaten D28 gereği API portunda 127.0.0.1'e açıktır; "yeni bir dış yüzey yok" iddiası `WEB_PORT`'un `127.0.0.1`'e bağlı olmasına dayanır (`compose.yml`); bu bağlama gevşetilirse `/metrics` web origin'i üzerinden ağa açılır ve bu karar yeniden değerlendirilmelidir. nginx konumu `/docs` ve `/schemas` önekinin tamamını (büyük/küçük harf fark etmeksizin) API'ye aktarır; böylece `/docs/`, `/Docs`, `/docs/a%2Fb` SPA'ya düşmez, API'nin JSON 404'ünü alır.

Dört yeni operasyon (`getApiDocs`, `getApiDocsAsset`, `getOpenApiSpec`, `getContractSchema`; etiket `documentation`) spec'e yazıldı; böylece `api-routes` kapısı istisna listesi olmadan çift yönlü eşitliği korur. Sayfa salt okunurdur, sır içermez, mutlak sunucu adresi yoktur. Sınır: Swagger UI paketi ~1,7 MB'dır; `.map` dosyaları gömülmedi (tarayıcı geliştirici araçları açıksa kaynak haritası 404 görür, işlev etkilenmez).

---

## 6. P4 sözleşme sertleştirmesi (içerik + onay)

P4 başlamadan önce üç rolün (test yazarı, Go, Vue) birbirine sormadan çalışabilmesi için sözleşmeler tarandı; boşluklar `docs/contracts/` ve `docs/requirements.md`'de kapatıldı. Burada yalnız davranışı belirleyen kararlar var.

### D46 — Tek içerik doğrulayıcısı: `Parse` yapıyı, `Validate` içeriği denetler
- **Bağlam:** `content.schema.json` `minLength: 1` taşıyor, ama `required` (alan var, değeri boş) ile `schema_violation` (anahtar eksik/fazla/yanlış tip) ayrı kodlar; Parse'ı "şemaya göre" yazan biri boş dizeyi `schema_violation` yapardı ve boş-değerli LLM taslağı hiç saklanamazdı.
- **Karar:** `Parse` yalnız yapı ve tip denetler (`minLength` uygulamaz); boş/yalnız boşluk değer `Validate`'te `required` olur. Alan başına en çok bir hata (öncelik `required` › `malformed_variable` › `unknown_variable` › uzunluk); hatalı alanda uzunluk denetlenmez. Değişken söz dizimi tablosu `go-interfaces.md` §content'te. Email gövdesi sınırı 5000 rune (dokümanda yok, VARSAYIM); yalnız doğrulama anında denetlenir, onayda yalnız SMS ve konu yeniden denetlenir.
- **Neden:** Aynı doğrulayıcı LLM çıktısı ve elle düzenleme için (REQ-APR-02); kodlar testlerin ve arayüzün tek anlaşma noktası.
- **Bedel:** Şema dosyasındaki `minLength` yalnız belge işlevi görür; sapma riski yorumla ve go-interfaces'le sınırlandı. Email gövdesi doldurma sonrası 5000'i onay anında yeniden denetlenmez (verinin sonradan değişmesi ve 5000'e dayanması gerekir; kabul).

### D37 — En kötü durum kümesi: eşleşen tüm müşteriler, kanal bazlı değil
Sahibin P1 kararı (README §9 #5) REQ-CNT-05'e açıkça yazıldı: değişken başına en uzun değer, kural eşleşmesinden sonra izin süzgecinden **önce** hesaplanır; SMS ve email aynı kümeyi kullanır. Neden: kanal bazlı küme daha dar olurdu ama izin sonradan değişince onaylanmış içerik limiti aşabilirdi; eşleşen küme izinden bağımsız bir üst sınırdır. Bedel: yalnız SMS izinlilerde olmayan uzun bir ad nedeniyle gereksiz yere kısa içerik istenebilir. Doğrulama anında stored `longest_values`, onay anında canlı müşteri verisi kullanılır; onayda yalnız `*_worst_case` kodları 422 üretir.

### D38 — Sürümler değişmez; numara tüm sürümler üzerinden artar
`version_number` = kampanyanın tüm satırlarında `MAX + 1` (saklanan `invalid` taslaklar dahil), yeniden kullanılmaz, segment revizyonuyla sıfırlanmaz; "en son sürüm" güncel revizyondaki en yüksek **validated** numaradır. Neden: `UNIQUE (campaign_id, version_number)` tek satırlık, kanıtlanabilir bir kural; `invalid` taslaklar da kimlik taşıdığı için (SSE `content_version_id`) numara tüketir. Bedel: doğrulanmış sürümlerde numara boşlukları (v1, v3) olabilir; arayüz yalnız en son sürümü gösterir, bu önemsiz.

### D39 — Aynı sürümü tekrar onaylamak idempotenttir ve denetimi tekrarlamaz
`approveContent`, durum zaten `approved` ve sürüm hâlâ en son ise 200 ve değişmemiş kampanyayı döner; yeni `approvals` satırı yok, `approved_at` sabit, onay-anı en kötü durum denetimi yeniden çalışmaz. Neden: çift tıklama/yeniden deneme güvenli olmalı, ama bu denetim müşteri verisi değiştiyse bir onayı sessizce 422'ye çevirirdi. Bedel: onaydan sonra veri değişimi ancak onay-anında (ilk onay) yakalanır; gönderim anındaki alıcı bazlı denetim (REQ-CNT-05) ikinci güvencedir.

### D40 — Düzenlemede iyimser eşzamanlılık: `X-Expected-Version` zorunlu (sahip kararı)
- **Bağlam:** İlk taslak "iyimser belirteç yok, kilitle sıralanır" demişti (sonuncu yazan kazanır, eskiler geçmişte kalır). Sahip bunu reddetti: eski bir sekme veya açık bir editör, başkasının (ya da yeniden üretimin) yeni sürümünü sessizce geçersiz kılmamalı.
- **Karar:** `editContent` isteği `X-Expected-Version` **başlığını zorunlu** taşır: kampanyanın en son `validated` sürümünün kimliği (güncel segment revizyonunda en yüksek `version_number`), yoksa `none`. `invalid` taslak sürüm sayılmaz: taslaktan düzeltme yapan editör son `validated` kimliğini (yoksa `none`) gönderir. Değer güncel en son sürümden farklıysa **409 `content_edit_conflict`**, hiçbir şey saklanmaz, gövde `error.current_version {id, version_number}` taşır (UI "yenile" sunar). Karşılaştırma ve ekleme tek transaction'da, `campaigns` satırı kilidi altında yapılır. Başlık eksik → 422 `schema_violation`, biçim bozuk → 422 `invalid_value` (yol `header.X-Expected-Version`). Yeniden üretim yeni `validated` sürüm yarattığı için açık editör 409 alır; başarısız yeniden üretim almaz. Başarılı bir isteğin ağ yüzünden tekrarı da 409'dur (sunucu tekilleştirmez); UI güncel sürümün içeriğini kendi metniyle karşılaştırıp eşitse başarı sayar.
- **Neden başlık, zarf değil:** Gövde `content.schema.json` (LLM aracı ve düzenleme ortak şeması) olarak kalır; zarf alan yollarını (`sms.body` → `content.sms.body`) ve UI/test eşlemelerini değiştirirdi. `If-Match` değil: standart 412/ETag anlamı ve tırnaklı değer gerektirirdi; ayrı kod ve gövde istendi. Ayrı kod (`stale_content_version` yeniden kullanılmadı): onay/gönderimdeki "bu sürüm artık en son değil" anlamıyla karışmasın ve gövdesi `current_version` taşısın.
- **Bedel:** `editContent` istek sözleşmesi kırıcı değişti (zorunlu başlık; `oasdiff` ERR, PR `api-breaking-ok` ister — uç nokta henüz uygulanmadığı için istemci yok); her istemci (UI, testler) taban sürümü izlemek zorunda; çakışma durumu için ek UI (§3.14 g). Eski ilk-taslak kararı (kilitle sıralama, iki düzenleme de başarılı) geçersizdir.
- **Çakışma ekranı (sahip kararı, 2026-10-27):** "Metnimi koru" (`keep-mine`) kalır: metin korunur, taban en son sürüme taşınır, sonraki kayıt yeni sürüm olur ("son yazan kazanır"; öteki sürüm silinmez). Ezme riski, düğmenin altında her zaman görünen açık bir uyarıyla (`editor-conflict-warning`) söylenir; panoya kopyalama ve birleştirme ekranı yapılmadı (tek kullanıcılı demo, nadir senaryo).

### D41 — Üretim kilidi `sendCampaign`'i de kapsar
`approved` bir kampanyada yeniden üretim akarken başka bir istemci gönderimi başlatırsa durum `sending` olurdu ve akış sonunda `content_validated` geçişi (yalnız `audience_ready|content_ready|approved`'dan geçerli) başarısız olurdu. Karar: kilit tutulurken gönderim de 409 `generation_in_progress` alır (openapi `SendConflict`'e eklendi; `oasdiff` yalnız `response-property-enum-value-added` uyarısı verir, kırıcı değildir). Bedel: takılı bir akış gönderimi en çok 120 sn engeller.

### D42 — SSE olay şemaları openapi bileşeni; tek kontrol sırası; bağlantıyı yeniden kurmama
Olay `data` gövdeleri `ContentDeltaEvent`/`ContentInvalidEvent`/`ContentValidatedEvent` bileşenleridir (ve durum akışı için `CampaignStatus`), eşleme `x-sse-events.schemas`; frontend tipleri `schema.gen.ts`'ten gelir, test yazarı her olayı bu şemalara karşı doğrular (D30'un "P4'te olay şemaları" sözü yerine getirildi). Kontrol sırası bütün işlemlerde aynıdır: biçimsiz path → senaryo başlığı → gövde (400/413/422) → servis (404, sonra işlemin sırası); yani bilinmeyen kampanyaya bozuk gövde 404 değil 400/413/422 döner (P3'te böyle uygulandı). İstemci terminal olay almadan koparsa **yeniden bağlanmaz**: her üretim bir LLM maliyeti ve yeni sürümdür, karar kullanıcıda. Kayıt transaction'ı başladıysa istemci kopması onu iptal etmez; görmediği sürüm `GET /api/campaigns/{id}` ile görünür (bu dar pencere testlerde iddia edilmez).

### D43 — İçerik akışı sonuçları iki katmanda test edilir; yeni mock senaryosu eklenmedi
14 senaryo E2E'de (HTTP + SSE + DB) yeter; senaryosu olmayan sonuçlar (200 sonrası erişim kaybı, 120 sn aşımı, boş değerli/bozuk-değişkenli LLM çıktısı, `internal_error`) için `llm.Client`/`ContentStream`/`ContentRun` genel arayüzlerinin test içi sahteleri kullanılır (`llm-tools.md` §3.5). Neden: kapalı senaryo listesi spec, TS tipleri, `/api/config` ve testlerde kopyalıdır; her ekleme dört yeri değiştirir. Bedel: bu sonuçlar E2E'de değil unit/servis entegrasyonunda kapsanır.

### D44 — Onarım turu biçimi sözleşmede; `no_tool_call` varyantı doğrulanmadı
D32'de gerçek API ile doğrulanan biçim (sahte `tool_use` + `is_error` `tool_result`, geçersiz JSON için `{}`) `llm-tools.md` §1.2'ye alındı. Önceki yanıt yalnız metinse (`no_tool_call`) tool_use olmadığından düz bir `user` metni gönderilir; bu varyant elle denenmedi. Gerçek istemci test edilmediğinden sözleşme davranışı bundan etkilenmez.

### D45 — Sayfa kompozisyonları: geçersiz taslak düzenlenebilir; hep en son sürüm onaylanır
PLAN §0.5 "pazarlamacı elle düzeltebilir" der; ui.md'de bunun yolu yoktu. `invalid` sonrası `content-edit-draft` taslak metinlerini editöre alır (hatalar satır içi, `dirty`), kaydet doğrulayıcıdan geçerek yeni `validated` sürüm üretir. `ApprovalPage` her zaman `latest_content_version`'ı onaylar/gönderir; `stale` yalnız 409 sonrası görünür (canlı bildirim yok). Bedel: ek iki testid (`content-edit-draft`, `content-empty`) ve sayfa durum tablosu (ui.md §3.23–3.24).

### D47 — Kanonik UUID, kilit penceresi ve `editContent` denetim sırası (P4 incelemesi)
- **Bağlam:** Bağımsız inceleme (PR #50) üç şey buldu: (a) `uuid.Parse` büyük harfli UUID'yi kabul eder ama dize olduğu gibi taşınınca bellek içi üretim kilidi (dize anahtarlı) ve sürüm kimliği karşılaştırmaları Postgres'ten (uuid tipi) farklı davranır: büyük harfli yol kilidi atlatır, büyük harfli `content_version_id` yanlış `stale_content_version` döner; (b) kilit denetimi (`busy`) satır kilidinden önce yapılır, onay ile üretim başlangıcı arasında bir pencere kalır; (c) README §5.4 (3) ile openapi/go-interfaces `editContent` sırası çelişir.
- **Karar:** (a) Kimlikler HTTP sınırında küçük harfli kanonik biçime çevrilir; servis de kanonik olmayan kimliği kanonikleştirir. (b) `Approve` ve `EditContent` kilit denetimini satır kilidi altındaki `decide` içinde yineler; `Send` için aynı kural P5'te zorunludur (P5 başlamadan önce üretim işaretçisi satır kilidiyle birlikte atomik olmalı). (c) `editContent` için sıra openapi ve go-interfaces'teki gibidir; README §5.4'e açık istisna yazıldı.
- **Bedel:** Üretim kilidi hâlâ süreç içidir (bkz. Bilinen sınırlar); bu karar yalnız tek süreç içindeki pencereyi kapatır.

### D48 — Düzenlenebilir SMS ve konu alanlarında giriş sınırda kırpılır (sahip kararı)
- **Bağlam:** Düzenleyici sınırın üstüne yazmaya izin veriyor ve sayaç `90/80 −10` gibi aşımı gösteriyordu; sahip bunu yanlış buldu ("80/80 olup daha fazla karakter yazmasına izin vermemeliyiz").
- **Karar:** `editor-sms-body` (160) ve `editor-email-subject` (80) şablon metnini rune sayısıyla limitte kırpar (yazma, yapıştırma, chip). Limiti zaten aşan LLM taslağı gerçek sayıyla gösterilir ve yalnız sayıyı artırmayan düzenlemeye izin verilir. API doğrulaması (422, REQ-APR-02) ve en kötü durum denetimi değişmez; arayüz sınırı yardımcıdır, güvence değildir.
- **Bedel:** Kullanıcı kısaltmak yerine kesilen metinle karşılaşabilir; yapıştırma sessizce kırpılır (bkz. ui.md §3.14 d2).

## Bilinen sınırlar ve riskler

| Konu | Durum |
|---|---|
| Strict modda segment aracının birleşik tip sayısı | Gerçek API ile doğrulandı, strict kabul edildi (D23) |
| SMS GSM-7/UCS-2 segment sayısı | Karakter sayımıyla yaklaşıldı (D10) |
| Pazarlamacı brief'i güncelleyemez | API brief güncellemeyi desteklemiyor; "brief'i düzenle" yeni kampanya başlatır |
| Gerçek ölçek (milyonlar) | PostgreSQL kuyruğu bu proje için yeterli, ölçek yolu `Queue` arayüzü (D1) |
| Yapılmayan bonuslar | Zamanlanmış gönderim, A/B varyantları, dile göre içerik: çekirdek akış sağlam olunca ayrı REQ'lerle; bitmeyenler README'de belirtilecek |
| Gerçek Claude istemcisi için otomatik test yok | Sözleşme gerçek istemciyi test binary'sinde yasaklar; strict araçlar ve onarım turu elle doğrulandı (D23, D32) |
| `seed.Generate` 9999'dan fazla kullanıcıyı kabul eder | Yalnız komut satırı reddeder (telefon bloğu 4 hane); mimar `go-interfaces.md`'ye hata ekleyebilir |
| Manifest sayıları `segment.Match` ile değil el yazısı değerlendirmeyle | P3'te `segment.Match`'e geçilecek, manifest aynı kalmalı |
| Email gövdesi 5000 sınırı onayda yeniden denetlenmez (D46) | Doğrulama anında denetlenir; yalnız SMS ve konu için onay-anı denetimi var |
| Kampanya kilidi tek süreç içinde çalışır | `generateSegment` kilidi süreç içidir; birden fazla API kopyası planlanırsa veritabanı kilidine (`SaveSegment`'in `FOR UPDATE`'i zaten korur) taşınmalı |
| Kitle taraması eşleşen satırları Go'ya çeker | Seed ölçeğinde (5000) yeterli; üç en uzun değer SQL'de toplanarak ölçeklenebilir |

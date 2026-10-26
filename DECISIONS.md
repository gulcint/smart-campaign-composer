# DECISIONS

Önemli kararlar ve nedenleri. Her faz kapısında güncellenir (`docs/PLAN.md` §3.2). Her karar: **bağlam → karar → neden → alternatifler/bedel**. "Doküman" case dokümanıdır; dokümanda olmayan her şey VARSAYIM olarak işaretlidir. Ayrıntılı gereksinimler `docs/requirements.md`, ad sözleşmeleri `docs/contracts/`.

Durum: **P0 ve P1 tamamlandı** (`p0-done`, `contracts-v1`). Kod yok; aşağıdakiler tasarım kararlarıdır. P2'den itibaren uygulama kararları eklenir.

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

---

## Bilinen sınırlar ve riskler

| Konu | Durum |
|---|---|
| Strict modda segment aracının birleşik tip sayısı | Gerçek API çağrısıyla P2'de denenecek; yedek karar var (D7) |
| SMS GSM-7/UCS-2 segment sayısı | Karakter sayımıyla yaklaşıldı (D10) |
| Pazarlamacı brief'i güncelleyemez | API brief güncellemeyi desteklemiyor; "brief'i düzenle" yeni kampanya başlatır |
| Gerçek ölçek (milyonlar) | PostgreSQL kuyruğu bu proje için yeterli, ölçek yolu `Queue` arayüzü (D1) |
| Yapılmayan bonuslar | Zamanlanmış gönderim, A/B varyantları, dile göre içerik: çekirdek akış sağlam olunca ayrı REQ'lerle; bitmeyenler README'de belirtilecek |

# Smart Campaign Composer

Pazarlamacı ne istediğini doğal dille yazar. Yapay zeka (Claude) hedef kitleyi ve SMS ile email metinlerini önerir, son kararı insan verir. Onaylanan mesajlar bir kuyruktan geçerek gönderilir ve gönderim canlı izlenir.

> "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

Ürün, kampanya hazırlayan pazarlamacılar içindir. Bilerek yapmadıkları: gerçek kimseye mesaj göndermez (alıcılar seed edilmiş sahte kullanıcılardır, gönderim kendi yazdığımız mock provider'adır) ve kimlik doğrulaması yoktur (yerel demo).

## Bir kampanya nasıl ilerler

1. **Brief.** Pazarlamacı isteğini yukarıdaki gibi düz cümleyle yazar.
2. **Kitle.** Claude brief'i bir segment kuralına çevirir (şehir, son 30 gün, sepet gibi alanlar). Arayüz kaç kişinin eşleştiğini, kaçına SMS ve email ile ulaşılabildiğini gösterir. İnsan kitleyi kontrol eder, gerekirse yeniden üretir.
3. **Mesajlar.** Claude SMS ve email metnini yazar; metin ekrana akarak gelir. Sistem uzunluk sınırlarını (SMS 160, email konusu 80 karakter) ve `{{first_name}}` gibi değişkenleri denetler.
4. **Onay.** İnsan metni önizler, düzenler ve onaylar. Onaydan sonra metni değiştirirse onay düşer, yeniden onaylanır.
5. **Gönderim ve izleme.** Mesajlar kuyruğa girer, mock provider'a hız sınırıyla gider; panel gönderilen, bekleyen ve başarısız sayılarını canlı gösterir.

Yapay zekanın çıktısı her adımda denetlenir. Geçersizse hata mesajıyla bir kez daha denenir, yine geçersizse kullanıcıya anlaşılır bir hata gösterilir:

```mermaid
sequenceDiagram
  actor M as Pazarlamacı
  participant W as web
  participant A as api
  participant L as Claude (LLMClient)
  participant D as PostgreSQL
  participant K as worker
  participant P as mockprovider

  M->>W: brief
  W->>A: kitle iste
  A->>L: segment kuralı üret
  L-->>A: kural (JSON)
  A->>A: şemaya göre doğrula
  opt geçersiz
    A->>L: hata mesajıyla tek onarım turu
    L-->>A: düzeltilmiş kural
  end
  A->>D: parametreli sorgu (kural SQL olmaz)
  D-->>A: kişi sayıları
  A-->>W: kitle
  M->>W: mesajları üret, düzenle, onayla
  A->>D: gönderimleri kuyruğa yaz
  K->>D: kuyruktan iş al
  K->>P: hız sınırıyla gönder
  K->>D: sonucu yaz
  A-->>W: canlı durum (SSE)
```

Mesaj üretimi de aynı doğrulama ve tek onarım turunu kullanır. Kampanya şu durumlardan geçer; kampanya düzeyinde hata durumu yoktur, tek tek mesaj hataları sayılarda görünür:

```mermaid
stateDiagram-v2
  [*] --> draft
  draft --> audience_ready: kitle hazır
  audience_ready --> content_ready: mesajlar doğrulandı
  content_ready --> approved: onaylandı
  approved --> content_ready: düzenlendi
  approved --> sending: gönder
  sending --> completed: hepsi sonuçlandı
  completed --> [*]
```

## Parçalar ve neden böyle

Tüm parçalar çalışır durumdadır (P5 sonu).

```mermaid
flowchart LR
  web["web (Vue)"]
  api["api"]
  llm["LLMClient<br/>Claude veya sahte"]
  db[("PostgreSQL<br/>kuyruk dahil")]
  seed["seed<br/>5000 sahte müşteri"]
  worker["worker<br/>kanal başına tek dispatcher"]
  mock["mockprovider"]

  web --> api
  api -- "içerik akışı (SSE)" --> web
  api -- "canlı durum akışı (SSE)" --> web
  api --> llm
  api --> db
  seed --> db
  worker -- "kuyruktan iş al, sonucu yaz" --> db
  worker -- "≤100/sn, retry" --> mock
```

Tasarım fikirleri (gerekçeler ve alternatifler: [`DECISIONS.md`](DECISIONS.md)):

- **Claude serbest metin değil, yapılandırılmış JSON döndürür.** Serbest metni ayrıştırmak kırılgandır. JSON'u şemaya göre doğrulayabilir, hatalıysa modele nedenini söyleyip düzelttirebiliriz.
- **Yapay zekanın çıktısı asla SQL olmaz.** Segment kuralı kapalı bir dildir: izinli alanlar, operatörler, değer tipleri. Backend kuralı doğrular ve kendi koduyla parametreli sorguya çevirir. Böylece model ne yazarsa yazsın veritabanına yetkisiz sorgu gitmez.
- **Kuyruk PostgreSQL'dedir.** Gönderilecek her mesaj bir satırdır; durumu, tekrar denemesi ve "aynı kişiye aynı mesaj iki kez gitmez" kuralı (kampanya, kanal, alıcı tekil) o satırda tutulur. Bu ölçekte ayrı bir kuyruk servisi gereksiz karmaşıklık olurdu.
- **İzni sistem uygular, model değil.** SMS ve email izinleri ayrıdır. İzni olmayan kişi kitle sorgusunda elenir; bunu modelin akıl yürütmesine bırakmak güvence vermez.
- **Sahte LLM ve mock provider vardır.** Anahtar yoksa sahte LLM devreye girer; mock provider rastgele hata, gecikme ve 429 üretir. Böylece testler ve demo anahtarsız çalışır, retry mantığı gerçekten denenir.

## Durum

Faz kapsamları: [`docs/PLAN.md`](docs/PLAN.md). Durum yalnızca koddan, git geçmişinden ve PR'lardan doğrulananlara göre yazıldı.

| Faz | Durum | Kapsam |
|---|---|---|
| P0 Kurulum | ✅ bitti | Repo, hook'lar, CI, plan |
| P1 Sözleşmeler | ✅ bitti | Gereksinimler, OpenAPI, şemalar, arayüz ve Go sözleşmeleri |
| P2 Seed + altyapı | ✅ bitti | Seed, migration'lar, compose, LLM fabrikası ve sahte LLM, mock provider, `make test` |
| P3 Kitle | ✅ bitti | Segment kuralı, kişi sayıları, brief ve kitle ekranları, API'nin sözleşmeden sapmaması için koruma |
| P4 İçerik + onay | ✅ bitti | SMS/email üretimi (SSE akışı), karakter limitleri, kişiselleştirme, düzenleme (sürümlü), onay, önizleme; içerik ve onay ekranları |
| P5 Gönderim + takip | ✅ bitti | Gönderim API'si, kuyruk ve worker (kanal başına tek dispatcher, hız sınırı, retry), canlı durum akışı (SSE) ve takip ekranı, veritabanında üretim kirası, "Claude düşünüyor" bekleme durumu, tüm ekranlarda responsive düzen |
| P6 Sertleştirme + teslim | ⬜ başlamadı | Uçtan uca test, güvenlik taraması, son README ve DECISIONS |
| Bonus | ⬜ başlamadı | Zamanlanmış gönderim, A/B varyantları, dile göre içerik |

## Çalıştırma

Gereken yalnızca Docker ve make.

```sh
docker compose up   # veritabanı, seed, api, mock provider, web arayüzü
make test           # tüm testler konteynerde, API key'siz
make help           # diğer komutlar
```

Yerelde `.env` dosyasına Claude anahtarı koyarsanız sistem gerçek Claude'u kullanır, koymazsanız sahte LLM çalışır. Örnek: [`.env.example`](.env.example).

Bugün denenebilecek olan: `docker compose up` bitince tarayıcıda <http://127.0.0.1:8081> adresini açın (bu port başka bir programda doluysa `.env` dosyasına `WEB_PORT=8082` gibi boş bir port yazın; API için `API_PORT` aynı şekilde çalışır; hangi portun açıldığını `docker compose ps` gösterir) ve örnek brief'i yazın ("Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"); sistem segment kuralını ve kanal başına kaç kişiye ulaşılacağını gösterir. Anahtar yoksa cevabı sahte LLM verir, `.env`'ye anahtar koyarsanız gerçek Claude cevaplar. Ardından aynı kampanya için SMS ve email içeriğinin akarak üretildiğini görür, metni düzenler ve onaylarsınız. "Gönder" ile teslimatlar kuyruğa yazılır; worker bunları kanal başına hız sınırıyla mock provider'a gönderir ve takip ekranı gönderilen, bekleyen ve başarısız sayılarını sayfa yenilenmeden gösterir. Kampanya tamamlanınca başarısız teslimat varsa "Başarısızları gör" bağlantısı listeyi açar. Ekranlar telefon (375 px), tablet (768 px) ve masaüstü (1280 px) genişliklerinde kullanılabilir.

## Nereden okunur

- [`DECISIONS.md`](DECISIONS.md): önemli kararlar, gerekçeleri, bilinen sınırlar
- [`docs/PLAN.md`](docs/PLAN.md): fazlar, git akışı, roller
- [`docs/requirements.md`](docs/requirements.md): gereksinim kataloğu
- [`docs/contracts/`](docs/contracts/README.md): API, veri ve LLM araç sözleşmeleri
- [`DESIGN.md`](DESIGN.md): görsel sistem ve ekranlar
- [`ai-sessions/INDEX.md`](ai-sessions/INDEX.md): temizlenmiş Claude session kayıtları
- [`CLAUDE.md`](CLAUDE.md): proje kuralları ve ajan talimatları

## Güvenlik ve sınırlar

- Claude anahtarı yalnızca yerel `.env` dosyasında durur; git'e girmez, hook'lar ve CI sızıntıyı tarar.
- Yapay zeka SQL yazmaz; kural doğrulanır ve parametreli sorguya kod tarafından çevrilir.
- Kanal izni olmayan kişiye o kanaldan mesaj gitmez.
- API ile dokümanı CI aynı tutar: OpenAPI sözleşmedir, testler her istek ve cevabı ona göre doğrular.
- Kimlik doğrulama yoktur; sistem yerel demo olarak yalnızca `127.0.0.1`'e açılır.
- Gönderim kanal başına saniyede en çok 100 istekle sınırlıdır; aynı kişiye aynı mesaj iki kez gitmez (veritabanı tekilliği + sağlayıcıya `Idempotency-Key`).
- Henüz yok: uçtan uca test ve güvenlik taraması (P6).
- Bonuslar (zamanlanmış gönderim, A/B varyantları, dile göre içerik) başlamadı. Gerçek Claude istemcisi için otomatik test yoktur, elle doğrulandı ([`DECISIONS.md`](DECISIONS.md)).

## Bilinen sınırlar

- Üretim kilidi kampanya satırında süreli bir kiradır (en çok 120 sn); bitişi API süreçlerinin saatine dayanır, süreçler arası saat kayması bitişi o kadar kaydırır ([`DECISIONS.md`](DECISIONS.md) D52).
- Kanal kilidi her partinin başında denetlenir; kilit kaybolursa eski gönderici en çok bir partiyi (varsayılan 50 istek) bitirir, o kısa sürede kanalda bir partilik fazla gönderim başlangıcı olabilir. Aynı mesaj yine iki kez gitmez (D53).
- Canlı durum akışı art arda 5 başarısız okumada (≈5 sn) kapanır; ekran `stale` olur ve yeniden bağlanır. Daha kısa kesintilerde sayaçlar birkaç saniye eski kalabilir (D54).
- Elle yapılan denemede gözlenen gönderim hızı kanal başına yaklaşık 50/sn'dir, 100 sınırının altında; otomatik bir hız ölçümü yoktur.
- Gönderim anı uzunluk denetimi (`CheckRendered`) bir alıcı için iki kanalın metnini birlikte denetler: SMS metni kişiselleştirmeyle 160'ı aşarsa o kişinin email teslimatı da `content_too_long` ile başarısız olur (veya tersi). Kanal bazlı denetim sözleşme değişikliği gerektirir.
- Dokunmatik boyutlar (44 px) ekran genişliğine göre (`max-lg:`, <1024 px) uygulanır, `pointer: coarse`'a göre değil; dar pencereli masaüstü de büyük hedef görür.
- İçerik ekranının eylem şeridi mobilde yapışkan değildir (sayfanın sonunda kalır); Onay ekranının `ApprovalBar`'ı yapışkandır.
- Swagger UI gibi etkileşimli API dokümanı yoktur (sahip kararıyla kaldırıldı, D36). API başvurusu: [`docs/contracts/openapi.yaml`](docs/contracts/openapi.yaml).
- Otomatik testler yalnızca `docs/requirements.md`'den türetilir (D50); yalnız sözleşmede yazan birçok davranış testsizdir.
- Gerçek Claude istemcisinin otomatik testi yoktur; şehir yazımı ve SMS uzunluğu gibi davranışlar elle doğrulanmalıdır.
- CI artık `web-image`, `compose-ports` ve `actionlint` kontrollerini çalıştırmaz (D27, D51).

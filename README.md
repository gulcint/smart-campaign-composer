# Smart Campaign Composer

Pazarlamacı ne istediğini doğal dille yazar; Claude hedef kitleyi ve SMS ile email metnini önerir. Son kararı insan verir, onaylanan mesajlar sahte bir servise gider ve gönderim ekranda canlı izlenir.

**Gerçek kimseye mesaj gitmez.** Alıcılar sahte kullanıcılardır, gönderim de sahte (mock) bir servise yapılır.

| Ne | Durum |
|---|---|
| Çekirdek akış: kitle → mesaj → onay → gönderim → izleme | ✅ |
| Bonus: zamanlanmış gönderim, kullanıcının diline göre içerik, A/B varyantları, CI | ✅ dördü de |

Ek olarak: geçmiş ve planlanmış kampanyaları gösteren bir panel ve yenilenmiş takip ekranı.

## Çalıştırma

Gerekenler: Docker (açık olmalı) ve `make`. API key gerekmez.

**1. Klonla ve klasöre gir.** Sonraki komutlar bu klasörde çalışır.

```sh
git clone https://github.com/gulcint/smart-campaign-composer.git
cd smart-campaign-composer
```

**2. (İsteğe bağlı) Gerçek Claude için key ekle.**

```sh
cp .env.example .env   # sonra ANTHROPIC_API_KEY satırını doldur
```

Key yoksa sahte Claude çalışır. Sahte Claude brief'i okumaz, her zaman aynı örnek kitleyi ve metni döner.

**3. Başlat.** İlk açılışta imajlar derlenir, biraz sürer. Veritabanı kurulur ve 5000 sahte kullanıcı yüklenir.

```sh
docker compose up
```

**4. Aç.** Arayüz: <http://127.0.0.1:8081>. Hazır olup olmadığını <http://127.0.0.1:8080/readyz> gösterir: `"status":"ready"` görünce hazırdır, `503` ise birkaç saniye bekle.

**5. Testler.** Kendi konteynerlerinde, ayrı bir veritabanıyla ve key'siz çalışır. Arayüz açıkken de çalıştırılabilir.

```sh
make test
```

**Durdurma**

```sh
docker compose down      # durdurur, veri kalır
docker compose down -v   # veriyi de siler; sonraki açılış her şeyi baştan kurar
```

**Sık görülen sorunlar**

- `port is already allocated`: port başka bir programda. Farklı port ver: `API_PORT=18080 WEB_PORT=18081 docker compose up`. Adresler de buna göre değişir.
- Docker'a bağlanamıyor: Docker kapalı. Docker Desktop'ı başlat.
- `no configuration file provided`: proje klasöründe değilsin, `cd smart-campaign-composer`.

## Ne denenir

Brief ekranındaki **"Örnek brief'i kullan"** butonu şu metni doldurur:

> "Son 60 günde alışveriş yapmayan müşterilere %15 indirimle geri dönmeleri için mesaj gönder. Sıcak bir dil kullan."

Case'teki İstanbul örneği de çalışır.

1. **Kitle:** Claude brief'ten bir kitle kuralı çıkarır. Ekran kaç kişinin eşleştiğini ve kaçının SMS/email alabileceğini gösterir.
2. **Mesajlar:** SMS ve email metni ekrana akarak yazılır. İstersen "A/B testi"ni açıp iki farklı mesaj aldırırsın. Kitlede İngilizce konuşan varsa İngilizce metin de gelir.
3. **Onay:** Metni önizle, gerekirse düzenle, onayla.
4. **Gönderim:** Hemen gönder ya da ileri bir tarihe planla.
5. **Takip:** Gönderim canlı izlenir ve "Kampanya tamamlandı" ile biter. Birkaç başarısız mesaj normaldir; sahte servis bilerek hata verir.
6. **Panel:** Brief ekranının altında geçmiş ve planlanmış kampanyalar listelenir.

![Tamamlanmış bir kampanyanın takip ekranı](docs/img/tracking-completed.png)

## Nasıl çalışır

```mermaid
flowchart LR
  browser["tarayıcı"] --> web["web (arayüz)"]
  web --> api["api"]
  api --> llm["Claude ya da sahte Claude"]
  api <--> db[("PostgreSQL<br/>veri + kuyruk")]
  worker["worker"] <--> db
  worker --> mock["sahte SMS/email servisi"]
```

Arayüz Vue, backend Go. Onaylanan mesajlar veritabanındaki bir kuyruğa yazılır; `worker` bunları sırayla sahte servise gönderir.

Temel güvenceler:

- **Claude'un cevabı kontrol edilir.** Claude yapılandırılmış JSON döner, backend doğrular. Geçersizse bir kez daha denenir, yine olmazsa kullanıcıya anlaşılır bir hata çıkar.
- **Claude'un yazdığı şey veritabanında komut olarak çalışmaz.** Kitle kuralı yalnız izinli alanlardan oluşur; sorguyu backend kendisi kurar (D8).
- **İzin yoksa mesaj yok.** SMS ve email izinleri ayrıdır; kontrolü sistem yapar.
- **Uzunluk sınırı:** SMS en çok 160, email konusu en çok 80 karakter. `{{first_name}}` gibi alanlar doldurulduktan sonra ölçülür.
- **Hız sınırı:** her kanal için saniyede en çok 100 mesaj.
- **Aynı kişiye aynı mesaj iki kez gitmez.** Hata alan mesaj artan aralıklarla en çok 5 kez denenir, sonra başarısız sayılır (D6).
- **API key gerekmez.** Testler ve `docker compose up` sahte Claude ile çalışır.

Kararların gerekçeleri: [`DECISIONS.md`](DECISIONS.md).

Son tam test: key'siz `make test` yeşil (Go testleri ve web testleri). Temiz kurulumda sistem ~15 sn'de hazır; İstanbul brief'iyle 720 mesaj ~23 sn'de işlendi, tekrar gönderim ve izinsiz gönderim 0.

## Bilinen sınırlar

- **A/B'de kazanan ölçülmez.** Sahte servis açılma ve tıklama üretmez; yalnız varyant başına gönderim sonucu görünür.
- **Gerçek Claude'un davranışı otomatik test edilmez.** Brief'i doğru anlama ve metin kalitesi sahte Claude ile ölçülemez.
- **Karşılığı olmayan koşul düşebilir.** Örneğin "25 yaş altı": verilerde yaş yok, Claude bu koşulu sessizce atlayabilir. Güvence, insanın kitle kartını kontrol etmesidir.
- **Bazı elle kontroller yapılmadı.** A/B ile İngilizce metin birlikte istendiğinde gerçek API'nin cevabı henüz elle denenmedi.
- **Saat yalnız Türkiye saati** (UTC+3).
- **Dil yalnız Türkçe ve İngilizce.** İngilizce metnin kalitesini sistem ölçmez; güvence insan onayıdır.
- **Ölçek demo içindir.** 5000 sahte kullanıcıyla rahat çalışır, milyonlarca kişi için tasarlanmadı.

## Daha fazla bilgi

- [`DECISIONS.md`](DECISIONS.md): kararlar ve nedenleri, tam sınırlar listesi
- [`docs/PLAN.md`](docs/PLAN.md): fazlar ve nasıl geliştirildiği (ayrı rollere sahip ajanlar)
- [`docs/requirements.md`](docs/requirements.md): isterler ve kabul ölçütleri
- [`docs/contracts/openapi.yaml`](docs/contracts/openapi.yaml): API uçları
- [`docs/contracts/db-schema.md`](docs/contracts/db-schema.md): veri modeli
- [`DESIGN.md`](DESIGN.md): arayüz tasarımı
- [`ai-sessions/INDEX.md`](ai-sessions/INDEX.md): Claude session kayıtları

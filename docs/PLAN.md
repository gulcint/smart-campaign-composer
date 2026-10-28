# Smart Campaign Composer — Uygulama Planı

> **Kaynak:** `Smart Campaign Composer Case.html` (repoda değil, `.gitignore`'da). Tek doğruluk kaynağı odur; repodaki karşılığı `docs/requirements.md`'dir.
> **İşaretler:** **[DOK]** dokümandan gelen ister/kısıt · **[SEN]** proje sahibinin kararı · **VARSAYIM** dokümanda olmayan, önerilen karar — her biri `DECISIONS.md`'ye gerekçesiyle yazılır.
> **Sürüm:** v4 · 2026-10-26 · Doküman karşılaştırmasındaki 13 düzeltme (#1–#13), bağımsız review bulguları (R1–R14) ve GitLab'dan GitHub'a (public) geçiş işlendi.

---

## 0. Kararlar ve mimari

### 0.1 Karar özeti

| Konu | Karar | Kaynak |
|---|---|---|
| Frontend | Vue.js · Vite + TypeScript + Pinia | [DOK] / VARSAYIM |
| Backend | Go | [SEN] (doküman: Go veya PHP) |
| LLM | Claude API [DOK] · Anthropic Go SDK, `LLMClient` arayüzü arkasında | [DOK] / VARSAYIM |
| Veritabanı | PostgreSQL | [SEN] |
| Kuyruk | PostgreSQL — `deliveries` tablosu kuyruğun kendisi | [SEN], gerekçe 0.3 |
| Repo | GitHub, **public**, adı `smart-campaign-composer` | [SEN] (doküman: GitHub veya GitLab; GitLab hesap doğrulaması yapılamadığı için GitHub'a geçildi, 1.6) |
| Commit dili | İngilizce | [SEN] |
| Case HTML | `.gitignore`'da, repoya girmez | [SEN] |
| Canlı akış | İçerik ekrana stream edilir ve gönderim durumu canlı gösterilir [DOK] · taşıma teknolojisi SSE | [DOK] / VARSAYIM (yalnızca SSE seçimi) |
| Metrics | Prometheus formatında `/metrics`, `/healthz`, `/readyz` | [DOK] endpoint'ler / VARSAYIM format |
| Auth | Yok — yerel demo; README'de belirtilir | VARSAYIM |

### 0.2 Bileşenler

```
web (Vue)  ──HTTP/SSE──▶  api (Go)  ──▶  PostgreSQL  ◀──  worker (Go: kanal başına dispatcher)
                            │                                   │
                            └──▶ LLMClient (claude | mock)       └──HTTP──▶ mockprovider (Go)
seed (Go, tek seferlik job)  ──▶  PostgreSQL
```

- `cmd/api`, `cmd/worker`, `cmd/mockprovider`, `cmd/seed` — ayrı binary'ler, aynı Go modülü.
- **Kampanya durum makinesi (VARSAYIM):** `draft → audience_ready → content_ready → approved → sending → completed` (kampanya düzeyinde `failed` yok; teslimat hataları `failed` teslimat olarak sayılır, kampanya yine `completed` olur; ayrıntı ve geri düşmeler `docs/requirements.md`). Onaysız `sending` yok [DOK]; onaydan sonra içerik düzenlenirse `content_ready`'e geri düşer (REQ-APR-03). P7'de `approved ⇄ scheduled → sending` eklenir (§9.2).

### 0.3 Kuyruk kararı: neden PostgreSQL

| İster | PostgreSQL ile | Redis ile |
|---|---|---|
| Aynı kişiye aynı mesaj iki kez gitmez [DOK] | `UNIQUE(campaign_id, channel, customer_id)` — garantiyi DB verir | Tekillik ayrı kurulur; durum yine Postgres'te → iki sisteme yazma, tutarsızlık riski |
| Onay → gönderim | Onay + teslimat kayıtları tek transaction | Atomik değil, outbox gerekir |
| Retry [DOK] | `attempts`, `next_attempt_at` kolonları | Gecikmeli kuyruk için sorted set |
| Canlı durum, metrics [DOK] | Kuyruk = durum tablosu; tek SQL, `LISTEN/NOTIFY` | Ayrı senkronizasyon |
| 100 msg/sn/kanal [DOK] | Kanal başına tek dispatcher (`pg_try_advisory_lock`) + token bucket (burst = 1, yani istek başlangıçları arasında en az 10 ms; her yarı-açık 1 sn'lik pencerede ≤ 100) | Dağıtık limiter daha doğal — Redis'in tek gerçek avantajı |
| Ölçek | Birkaç bin kullanıcı için fazlasıyla yeterli | Milyonlarca mesajda avantajlı |
| Kurulum ve test | Tek bağımlılık | Ek servis + tutarlılık testleri |
| Bonus: zamanlanmış gönderim | Kampanyada `scheduled_at` + worker'da zamanı gelen kampanyayı başlatan döngü; teslimatlar zamanı gelince aynı gönderim transaction'ıyla doğar (P7, §9.2) | Ayrı mekanizma |

- İşler `FOR UPDATE SKIP LOCKED` ile alınır. Provider'a `Idempotency-Key` (= delivery id) gönderilir; mock provider bununla tekilleştirir → "provider kabul etti, worker durumu yazamadan çöktü" durumunda retry tekrar göndermez.
- Kendi küçük implementasyonumuz (River gibi kütüphane yerine) — VARSAYIM; gerekçe: `deliveries` tablosu zaten gerekli ve review görüşmesinde her satır savunulabilir olmalı.
- `Queue` arayüzü üzerinden Redis Streams / NATS / Kafka'ya geçiş yolu `DECISIONS.md`'de not edilir.

### 0.4 Retry stratejisi (VARSAYIM — doküman tasarımı bize bırakıyor)

| Provider yanıtı | Davranış |
|---|---|
| 2xx | `sent` |
| 429 | `Retry-After` varsa ona uy, yoksa backoff; hata denemesi sayılmaz, ayrı 429 bütçesinden sayılır |
| 5xx, 408, timeout, bağlantı hatası | Üstel backoff + full jitter (taban 1 sn, tavan 60 sn) |
| 4xx (429 ve 408 hariç) | Retry yok → `failed` |
| Teslimatın 5. başarısız denemesi (ilk deneme dahil) | `failed` (dead), dashboard'da görünür |

- Retry'lar da kanal başına 100/sn limitine dahildir.
- **429 ayrı bütçeyle sayılır (VARSAYIM):** mock provider bilerek 429 döndürdüğü için 429'lar 5'lik hata bütçesini tüketmez. Ayrı tavan: 20 adet 429 veya teslimatın ilk gönderim denemesinden itibaren 10 dakika. Aşılırsa `failed`. Amaç, kasıtlı 429'ların toplu `failed` üretmesini önlemek.

### 0.5 Stream + JSON doğrulama protokolü (R5)

Doküman hem "SMS ve email içeriği ekrana stream edilerek akar" hem "Claude'un çıktısı yapılandırılmış (JSON) olmalı ve backend'de kontrol edilmeli" diyor. İkisi şöyle birleşir (VARSAYIM, P1'de sözleşmeye yazılır):

- **Segment kuralı:** stream edilmez. Claude tool use ile JSON üretir, backend tamamını alıp şemayla doğrular, sonra sayıyı döner.
- **İçerik:** Claude tool use ile JSON üretir (`sms.body`, `email.subject`, `email.body`). Anthropic API tool girdisini parça parça (`input_json_delta`) stream eder. Backend parçaları biriktirir ve alan bazında metin olarak SSE'ye aktarır.
- **SSE olayları:** `delta {channel, field, text}` → `validated {content_version_id}` veya `invalid {errors[]}`.
- **Doğrulama noktası:** stream bittiğinde, tam JSON üzerinde, düzenlemelerle aynı `content.Validate` ile. Stream sırasında görünen metin **taslaktır**; `validated` gelmeden onay butonu pasiftir.
- **Doğrulama başarısızsa:** backend hataları Claude'a geri vererek bir kez yeniden üretir. İkinci seferde de başarısızsa arayüz hatayı alan bazında gösterir. Pazarlamacı elle düzeltebilir veya yeniden üretebilir.
- Mock LLM aynı olay sırasını deterministik üretir. Stream'in yarıda kesilmesi ve bozuk JSON fixture'ları bu protokole göre test edilir.

---

## 1. Fazlar, takvim ve git akışı

### 1.1 Fazlar — dikey dilimler, 7 gün

Doküman "her şeyi yapmak yerine çekirdek akışı sağlam yap" diyor; frontend %20 ağırlıkta. Bu yüzden her dilim backend + ekran + testleriyle birlikte biter (düzeltme #9, #10).

| Gün | Faz | İçerik | Kapı |
|---|---|---|---|
| 1 | **P0 Kurulum** | **İlk iş: bu planlama session'ının export'u.** `git init`, `.gitignore`, `.gitattributes`, `.dockerignore`, hook'lar, `.github/workflows/ci.yml` iskeleti, `Makefile` iskeleti, `.env.example`, `CLAUDE.md`, `ai-sessions/INDEX.md` | **GitHub public repo sahibin onayıyla oluşturulur**, `main` ruleset'i kurulur |
| 1 | **P1 Sözleşmeler** | `docs/requirements.md` (REQ ID'leri), OpenAPI, LLM JSON şemaları, stream protokolü (0.5), izinli şablon değişkenleri listesi, segment DSL, `docs/contracts/go-interfaces.md`, **UI sözleşmesi** (`docs/contracts/ui.md`: ekranlar, bileşen adları, props, `data-testid`'ler), DB şema taslağı | Sahip onayı → `contracts-v1` etiketi |
| 2 | **P2 Seed + altyapı** (`slice/p2-foundation`) | Seed üreticisi + manifest, compose, migration'lar, health, `LLMClient` fabrikası (auto/real/mock), mock LLM iskeleti, `make test` konteyner altyapısı | REQ-DLV-01, REQ-DLV-02, REQ-AI-02, REQ-AI-03, REQ-AI-04, REQ-AI-05, REQ-TRK-03, REQ-SEC-02, REQ-SEED-01, REQ-SEED-02 testleri yeşil |
| 3 | **P3 Kitle dilimi** | Segment kuralı üretimi, doğrulama, parametreli sorgu, kanal bazında kişi sayısı + ekran | REQ-AUD (00–05), REQ-AI-01 (segment) testleri yeşil |
| 4 | **P4 İçerik + onay dilimi** | SMS/email üretimi (stream protokolü 0.5), limitler, kişiselleştirme, düzenleme, yeniden doğrulama, sürümlü onay + ekranlar | REQ-CNT (01–05), REQ-APR, REQ-AI-01 (içerik), REQ-UI-01 testleri yeşil |
| 5 | **P5 Gönderim + takip dilimi** | **İlk iş, `Send`'den önce: içerik üretim kilidi veritabanına taşınır** (D41, D47; kampanya satırındaki kiraya taşındı, D52). Üretim işareti veritabanında tutulur ve kampanya satır kilidiyle atomik denetlenir; `Send` (üretim, düzenleme ve onayla birlikte) aynı transaction'da kontrol eder, kural birden fazla API sürecinde de geçerlidir (REQ-SND-01). Ardından `deliveries` kuyruğu, dispatcher, rate limit, retry, idempotency, mock provider, SSE dashboard, metrics + ekran (ActivityFeed yok, yalnız sayaçlar; D56). Test yığınına worker trafiği almayan ikinci mock provider (`mockprovider-solo`, D55). İçerik ekranında "Claude düşünüyor" bekleme durumu (ilk parçadan önce). **Responsive tasarım güncellemesi** (DESIGN.md V13) P5 içinde kendi branch'inde, `feat/p5-responsive` (DESIGN.md §8: tablet 768–1023 kuralları, Brief/Kitle/Onay ekranlarının mobil düzeni, tablo/liste davranışı, `viewport` meta; tüm ekranlarda uygulama) | REQ-SND (01–05), REQ-TRK-01/02, REQ-MOCK, REQ-SEC-01 testleri yeşil; responsive (`feat/p5-responsive`): 375/768/1280 px genişliklerinde tüm ekranların ekran görüntüsü kanıtı PR'da (REQ yok, otomatik test yok) |
| 6 | **P6 Sertleştirme + teslim** | Uçtan uca test, güvenlik taraması, `README.md`, `DECISIONS.md`, session export'ları, değerlendirici erişimi · **Kişisel bilgi temizliği** (sahip isteği, 2026-10-28): izlenen dosyalar ve `ai-sessions/` export'larından ad-soyad, kişisel e-posta (gmail) ve yerel bilgisayar yolları (`/Users/<kullanıcı>/…`) temizlenir; temizleme betiği (`make sanitize-sessions`) ve hook desenleri bu üç türü kapsayacak şekilde genişletilir. Commit meta verisi: GitHub'da e-posta gizliliği açılır ki squash merge commit'leri noreply adresiyle yazılsın; geçmişteki commit'lerin yazar adı/e-postası **yeniden yazılmaz** (sahip kararı, 2026-10-28, `DECISIONS.md` D58). GitHub kullanıcı adı (`gulcint`) repo adresi ve Go modül yolunun parçası olduğu için kalır | REQ-DLV-03, REQ-DLV-04 + REQ eşleme raporu boş + doğrulayıcı onayı + teslim kontrol listesi; kişisel bilgi taraması (izlenen dosyalarda `git grep` ile ad, e-posta, `/Users/` yolu; yeni commit'lerde `git log --format='%an <%ae>'`) temiz |
| 7 | **P7 Bonus** (`slice/p7-schedule`) | Case'in "Bonus (isteğe bağlı)" listesinden iki kırım (§9): **P7a Zamanlanmış gönderim** — onaylı kampanya ileri bir zamana planlanır, iptal/yeniden planlanır, zamanı gelince worker gönderimi başlatır; Onay ekranında "şimdi / ileri tarihte", Takip ekranında planlanan saat + geri sayım (REQ-SCH-01..04) · **P7b CI pipeline ✅** — P0'da kuruldu, P2–P6'da genişletildi ve sadeleştirildi (§1.2, §2.3, D27, D51); P7'de yeni iş yok. A/B varyantları ve dile göre içerik **kapsam dışı** (sahip eklemedikçe) | P7a: REQ-SCH (01–04) testleri yeşil, `make db-roundtrip` yeşil, §3.2 listesi; P7b: `main` ruleset'i `ci-ok`'u zorunlu tutuyor (kanıt: son merge'lerin `ci-ok` sonucu) |

Her faz sonunda `main` üzerinde `p<N>-done` etiketi.

### 1.2 Git akışı — dilim branch'leri

Trunk-based; `main` her zaman yeşil. Testler implementasyondan önce yazıldığı için kırmızı testler `main`'e girmemeli. Bu yüzden her dilim için bir entegrasyon branch'i açılır:

```
main
 └─ slice/p3-audience                 (dilim entegrasyon branch'i)
     ├─ test/p3-audience-rules   ──PR──▶ slice   (test yazarı; kırmızı testler serbest)
     └─ feat/p3-segment-validator ─PR──▶ slice   (kodu yazan; testleri yeşile çevirir)
 slice/p3-audience  ──PR──▶ main      (tam pipeline yeşil olmadan merge yok)
```

- P2'den itibaren her faz bu akışı izler (P2 dahil: `slice/p2-foundation`). P0 ve P1'de test yoktur, kod da yoktur.
- Test yazarının worktree'si **sparse checkout** ile açılır: yalnızca `docs/`, `testdata/`, `test/` ve test dosyası desenleri (`**/*_test.go`, `web/tests/**`). Önceki fazlardan `main`'e girmiş implementasyon (`cmd/`, `internal/`, `migrations/`, `web/src/`) worktree'de **bulunmaz** (R1).
- `*_test.go` dosyaları Go gereği paketlerin yanında durur. Sparse checkout bu dosyaları getirir ama yanlarındaki `.go` implementasyon dosyalarını getirmez. Test yazarı testleri derleyip çalıştırmaz; çalıştırma kodu yazanın ve CI'ın işidir.
- Her PR'da aynı pipeline tetiklenir (Actions public repoda sınırsız); `main`'e yalnızca `ci-ok` yeşilken merge edilir. Yalnızca doküman değiştiren PR'da (`ai-sessions/**`, `docs/contracts/` dışındaki `docs/**`, `docs/contracts/` ve `web/` dışındaki `*.md`) `detect` job'ı `heavy=false` verir: build/test job'ları (go-lint, web, sql-lint, db-roundtrip, make-test, govulncheck) atlanır, politika ve sır job'ları her zaman çalışır, `ci-ok` yine raporlar. `main`'e push'ta her şey çalışır. Owner kararıyla (2026-10-27) `web-image` (üretim nginx imajı build + `nginx -t`), `compose-ports` (db/mockprovider host portu yayınlamaz) ve `actionlint` (workflow sözdizimi) CI'dan çıkarıldı; bunlar artık CI'da koşmaz (`actionlint` yalnızca yerel pre-commit hook'unda kalır). Ayrı `go-unit` job'ı yok: make-test aynı paketleri `-race` ile, `-short` olmadan koşar.
- `main`'e squash merge; `slice/*` içinde merge commit serbest.

### 1.3 Branch isimlendirme

`<tür>/p<faz>-<kısa-açıklama-kebab>` — küçük harf, ASCII, ≤50 karakter.

| Tür | Kim açar |
|---|---|
| `slice` | Sahip / mimar |
| `test` | Yalnızca test yazarı |
| `feat`, `fix`, `refactor` | Yalnızca kodu yazan |
| `docs`, `chore`, `ci`, `sec` | Mimar / kodu yazan |

Zorlama: pre-push hook + CI branch adı job'ı.

### 1.4 Commit mesajı şablonu (Conventional Commits, İngilizce)

`.gitmessage` → `git config commit.template .gitmessage`

```
<type>(<scope>): <imperative summary, ≤72 chars>

<WHY it changed and which behavior is affected; wrap at 72>

Refs: REQ-SND-03, REQ-SND-04
Phase: P5
```

- Kapsamlar: `audience`, `content`, `approval`, `delivery`, `tracking`, `llm`, `seed`, `db`, `ui`, `infra`, `ci`, `docs`.
- `Refs:` `feat`, `fix`, `test` türlerinde zorunlu.
- `main`'deki squash commit mesajı **PR başlığından** gelir → PR başlığı da bu biçime uymak zorunda (CI job'ı).

### 1.5 `.gitignore` kuralları

Dört kaynağa göre belirlendi: (a) yığının ürettiği artifact'ler, (b) sır içeren dosyalar, (c) yerel/kişisel ortam, (d) araç state'i. İlke: üretilebilen ve sır içeren hiçbir şey commit edilmez.

| Grup | Kurallar | Neden |
|---|---|---|
| Sırlar [DOK: .env] | `.env`, `.env.*`, `!.env.example`, `*.pem`, `*.key` | Key yalnızca `.env`'de |
| Case dokümanı [SEN] | `Smart Campaign Composer Case.html` | Insider'ın dokümanı |
| Go | `/bin/`, `*.exe`, `*.test`, `*.out`, `coverage.*` | Derleme/test çıktısı |
| Frontend | `node_modules/`, `dist/`, `.vite/`, `*.local` | npm/Vite çıktısı |
| Docker/DB | `pgdata/`, `*.dump`, `*.sql.gz` | Volume ve dump |
| Araç state'i | `.omc/`, `.claude/settings.local.json` | Yerel state; `.claude/settings.json` paylaşılır |
| OS/IDE | `.DS_Store`, `.idea/`, `.vscode/*`, `!.vscode/extensions.json` | Kişisel ortam |
| Log | `*.log`, `logs/` | PII/sır sızdırabilir |

`.dockerignore` aynı sır kurallarını içerir. `.gitattributes`: `* text=auto eol=lf`.

### 1.6 GitHub repo ve sunucu tarafı zorlama

**Geçiş notu:** Plan önce GitLab Free private repo öngörüyordu. GitLab.com paylaşımlı runner'ları hesap kimlik doğrulaması (telefon/kart) istedi ve doğrulama yapılamadı; ilk pipeline hiç job çalıştırmadan düştü. GitLab projesi kalıcı olarak silindi. Repo GitHub'da **public** [SEN]; hassas veri repoda ve remote'ta tutulmuyor.

**Oluşturma (sahibin onayıyla):** `gh repo create smart-campaign-composer --public --source . --remote origin` (push yapmaz). İlk push sırası: **önce `main`** — sahip, bir kerelik `--no-verify` ile (pre-push hook'u ve Claude guard'ı `main`'e push'u bilerek reddeder) — sonra branch'ler. GitLab'da ters sıra, ilk push edilen branch'in varsayılan ve korumalı olmasına yol açmıştı.

**`main` ruleset (public repoda ücretsiz):**
- PR zorunlu; required approvals 0 (tek kişi kendi PR'ını onaylayamaz, reviewer raporu PR'a eklenir); conversation resolution zorunlu.
- Required status check: `ci-ok` (tüm job'ları toplayan kapı job'ı); branch güncel olmalı.
- Force push ve silme yasak; doğrusal geçmiş.
- Bypass listesi boş: kurallar repo sahibine de uygulanır.

**Merge ayarları:** yalnızca squash; squash başlığı = PR başlığı, gövde = commit mesajları → commit'lerdeki `Refs:` satırları `main`'e taşınır. CI, `feat`/`fix`/`test` başlıklı PR'da en az bir commit'te `Refs:` olmasını şart koşar. Merge sonrası branch silinir.

**Güvenlik özellikleri (public repoda ücretsiz):** secret scanning + push protection, Dependabot alerts ve security updates. CodeQL (Go, TypeScript) P2'de kod gelince eklenir.

**Public repo olmasının sonuçları:**
- Değerlendiricilere ayrıca erişim vermek gerekmez.
- Commit yazar e-postası GitHub noreply adresidir (`<id>+gulcint@users.noreply.github.com`, repoya özel git ayarı); geçmiş ilk push'tan önce bu adresle yeniden yazıldı.
- `ai-sessions/` ve tüm dokümanlar herkese açık: temizleme + gitleaks + elle kontrol adımları zorunlu.
- Actions dakikaları public repoda sınırsız.

---

## 2. Git hook'ları

Araç: `lefthook` (VARSAYIM), `make hooks` ile kurulur.

### 2.1 pre-commit — hızlı, staged dosyalar, hedef <10 sn

| Kontrol | Araç | Kapsam |
|---|---|---|
| Format | `gofmt -l`, `goimports -l` | staged `*.go` |
| Statik analiz | `go vet`, `golangci-lint run --new-from-rev=HEAD` (gosec, errcheck, staticcheck, sqlclosecheck, rowserrcheck) | değişen paketler |
| Modül hijyeni | `go mod tidy` + `git diff --exit-code go.mod go.sum` | `go.mod` değiştiyse |
| Sır taraması | `gitleaks protect --staged` | tüm staged |
| Yasak dosyalar | `.env`, `*.pem`, `pgdata/`, case HTML stage edilmişse ret | tüm staged |
| SQL lint | `sqlfluff lint --dialect postgres` | staged `*.sql` |
| Migration çifti | Her `NNNN_x.up.sql` için `NNNN_x.down.sql`; numara artan ve çakışmasız | `migrations/` |
| Migration değişmezliği | `origin/main`'de olan migration dosyası değiştirilemez | `migrations/` |
| Tehlikeli SQL | `DROP TABLE/COLUMN` için `-- allow-destructive` işareti zorunlu | `migrations/` |
| SQL birleştirme | gosec G201/G202 (`fmt.Sprintf` ile SQL) | staged `*.go` |
| ai-sessions | gitleaks + `sk-ant-`, `/Users/<kullanıcı>`, gerçek email desenleri | `ai-sessions/**` |
| Frontend | `eslint` (`vue/no-v-html` error), `prettier --check` | staged `web/**` |

**commit-msg:** şablon biçimi + `Refs:` zorunluluğu.

### 2.2 pre-push — kapsamlı, hedef <2 dk

| Kontrol | Araç |
|---|---|
| Derleme | `go build ./...` |
| Unit testler | `make test-unit` (`-race -short -count=1 -shuffle=on`, `ANTHROPIC_API_KEY` silinmiş) |
| Tam lint | `golangci-lint run ./...` |
| Bağımlılık açıkları | `govulncheck ./...` |
| Migration gidiş-dönüş | Geçici Postgres'te `up → down → up`, şema diff'i boş (Docker yoksa uyarı; CI'da zorunlu) |
| Sır taraması | `gitleaks detect --log-opts="origin/main..HEAD"` |
| Branch politikası | `main`'e doğrudan push ret; branch adı 1.3 desenine uymalı |
| Rol ayrımı | `feat/*`/`fix/*` test dosyalarına, `test/*` implementasyon dosyalarına dokunursa uyarı (CI'da hata) |

### 2.3 Hook'lar atlanabilir — asıl zorlayıcı CI + protected branch

- `--no-verify` ile atlanabilir veya hiç kurulmamış olabilir. Hook'lar hızlı geri bildirimdir, güvenlik sınırı değildir.
- **Asıl zorlayıcı:** GitHub Actions tüm kontrolleri yeniden çalıştırır (yalnızca doküman değiştiren PR'da build/test job'ları atlanır, bkz. §1.2; + `make test`, compose duman testi, tüm geçmişte gitleaks, frontend testleri, `npm audit`); `main` ruleset'i `ci-ok` kırmızıyken merge'ü sunucu tarafında engeller. Ajanın merge yetkisi buna ek bir yerel katmandır: `guard-git.sh`, `gh pr merge`'e yalnızca o PR'da `ci-ok` yeşilken, `--squash` ve açık PR numarasıyla izin verir; `--admin` yasaktır. Secret scanning push protection, sır içeren push'u GitHub tarafında reddeder.
- Ek katman: Claude Code `PreToolUse` hook'ları ajanların `main`'e push yapmasını engeller.
- CI dokümanda **bonus** olarak geçiyor (düzeltme #12) — README'de bonus olarak belirtilir.

---

## 3. Roller

Kodu yazan, review eden ve testi yazan ayrı bağlamlardır; hiçbiri kendi işini onaylamaz.

| Rol | Kim | Okuyabildikleri | Yazabildikleri | Sorumluluk |
|---|---|---|---|---|
| **Sahip** | Proje sahibi (insan) | Her şey | Karar | Sözleşme onayı (`contracts-v1`), repo oluşturma, VARSAYIM kabulü. Merge yetkisini, `ci-ok` yeşilken orkestratöre devretti (2026-10-26); **her PR'ı okuyup açıklayabilmek** (merge'ü beklemeden, faz kapanışına kadar) |
| **Mimar** | `architect` / `planner` | Doküman, kod | `docs/` | REQ kataloğu, OpenAPI, şemalar, DSL, Go arayüz imzaları, `DECISIONS.md` — koddan önce |
| **Test yazarı** | `test-engineer`, **ayrı Claude Code session'ı + sparse checkout'lu ayrı worktree** | Yalnızca `docs/requirements.md` (tek kaynak, istisnasız); `docs/contracts/**`, `DECISIONS.md` ve case dokümanı okunamaz (guard engeller) | `**/*_test.go`, `test/**`, `testdata/**`, `web/tests/**` (frontend testleri `web/src` dışında) | Yalnızca `docs/requirements.md` kabul ölçütlerinden test; REQ'i olmayan veya somut adı/yolu/alanı REQ'de yazmayan davranış için test yazmaz, REQ cümlesi önerisiyle "REQ eksik" diye sahibe bildirir; implementasyona asla bakmaz |
| **Kodu yazan** | `executor` | Doküman, sözleşmeler, testler | `cmd/`, `internal/`, `migrations/`, `web/src/` | Testleri yeşile çevirir; test dosyalarını değiştiremez. **Tek istisna:** `testdata/seed/manifest.json`'ı seed kodu üretir, kodu yazan commit'ler; test yazarı yazamaz, yalnızca okur (`docs/contracts/seed.md`) |
| **Reviewer** | `code-reviewer` (+ güvenlikte `security-reviewer`) | Her şey | PR yorumu/raporu | Kodu sözleşmeye, testleri dokümana, ikisini güvenlik listesine göre inceler |
| **Doğrulayıcı** | `verifier` | Her şey | Rapor | Faz kapısında kanıtla son onay |

### 3.1 "Test yazarı koda bakmaz" kuralının uygulanışı

1. Sözleşme → test → kod sırası; test worktree'si sparse checkout ile implementasyon dizinleri olmadan açılır (1.2).
2. Case HTML `.gitignore`'da olduğu için worktree'de bulunmaz; test yazarına verilmez, okunması engellidir (madde 7).
3. Testler yalnızca bir REQ ölçütünün kendisinin adlandırdığı yüzeylere yazılır (HTTP uç noktası, SSE olayı, Go fonksiyonu, DB sonucu, `data-testid`); sözleşme dosyaları test yazarına kapalıdır (madde 7). Frontend testleri bileşenleri yalnızca REQ'de adı geçen props ve `data-testid`'ler üzerinden sürer (R2).
4. CI rol job'ı: `test/*` implementasyona, `feat/*`/`fix/*` testlere dokunamaz.
5. Çatışmada test koda uydurulmaz: sahip karar verir → mimar `docs/`'u düzeltir → test yazarı testi buna göre günceller, ya da kod düzeltilir.
6. Yazmadan önce mevcut testi bul: test yazarı REQ ID'sini yalnızca test yollarında arar (Grep aracı `glob: "*_test.go"` / `path: "web/tests"` ya da hedefi test yolu olan shell `grep`/`rg`; guard `cmd/`, `internal/`, `migrations/`, `web/src/`, `.` ve stdin'i engeller). Test varsa yeni dosya açılmaz, mevcut test genişletilir (§4.5 dosya düzeni).
7. **Tek kaynak: `docs/requirements.md`, istisnasız.** Test vakaları ve testte kullanılan her ad/yol/alan yalnızca REQ kabul ölçütlerinden gelir; `docs/contracts/**`, `DECISIONS.md` ve case dokümanı ne vaka ne yardımcı kaynaktır, `scripts/claude/test-writer-guard.sh` bunların okunmasını engeller. Bir davranışı ifade etmek için gereken somut ad/yol/alan `docs/requirements.md`'de yoksa test yazılmaz; test yazarı REQ cümlesi önerisiyle "REQ eksik" diye sahibe döner, sahip/mimar `docs/requirements.md`'ye ekler, sonra test yazılır. **Sonuç: yalnızca sözleşmede veya `DECISIONS.md`'de olan davranışlar için test yazılamaz** — önce REQ'e çevrilmeleri gerekir. Review bulgusu test gerektiriyorsa önce REQ ölçütü eklenir. Çözülebilir bir `// REQ-…` yorumu (ID `docs/requirements.md`'de, test o ölçütü sınıyor) taşımayan test review'da reddedilir.
8. Dürüst sınır: ajanın dosya okuması her ortamda tam engellenemez; worktree izolasyonu + CI path kontrolü + reviewer'ın "bu test implementasyon detayına mı bağlı?" kontrolü birlikte uygulanır.

### 3.2 Faz kapısı kontrol listesi

- [ ] Fazın REQ ID'lerinin hepsi en az bir testle eşli
- [ ] `slice/*` → `main` PR'ında tam pipeline yeşil, tüm conversation'lar çözülmüş
- [ ] Reviewer raporu PR'da
- [ ] Doğrulayıcı kanıtı (komut + çıktı) PR'da
- [ ] **Sahip PR'ı okudu ve açıklayabilir** (düzeltme #11). Merge bunu beklemez; `ci-ok` yeşilse orkestratör merge eder, sahibin okuması faz kapanışından önce tamamlanır
- [ ] `DECISIONS.md` bu fazın kararlarıyla güncel
- [ ] Bu fazın **tüm session'ları** export edildi, temizlendi, `ai-sessions/INDEX.md`'ye eklendi

---

## 4. Gereksinimler ve testler

### 4.1 Gereksinim kataloğu (`docs/requirements.md`)

> **Güncel ve bağlayıcı katalog `docs/requirements.md`'dir** (kabul ölçütleri ve dokümandan birebir alıntılarla). Aşağıdaki tablo planlama anındaki özetidir; P1'de eklenen ID'ler: REQ-AUD-05, REQ-CNT-05, REQ-AI-04, REQ-AI-05, REQ-UI-01, REQ-SEC-01/02, REQ-SEED-01/02, REQ-SND-05, REQ-DLV-04.

| ID | Gereksinim | Kaynak |
|---|---|---|
| REQ-AUD-00 | Claude brief'ten yapılandırılmış segment kuralı üretir | [DOK] akış 1 (düzeltme #13) |
| REQ-AUD-01 | Segment kuralı backend'de doğrulanır | [DOK] |
| REQ-AUD-02 | LLM çıktısı doğrudan SQL olarak çalıştırılmaz | [DOK] |
| REQ-AUD-03 | Arayüz kuralın kaç kişiye gideceğini gösterir; sayı **kanal bazında, izin filtresinden sonra** verilir (SMS: n, Email: m) | [DOK] / VARSAYIM kanal bazı (R11) |
| REQ-AUD-04 | İzni olmayana o kanaldan mesaj gitmez | [DOK] |
| REQ-CNT-01 | SMS ≤160 karakter (ölçüm: 4.2) | [DOK] |
| REQ-CNT-02 | Email konusu ≤80 karakter | [DOK] |
| REQ-CNT-03 | `{{first_name}}` gibi değişkenler desteklenir; izinsiz değişken reddedilir. İzinli liste P1'de sabitlenir; öneri: `first_name`, `last_name`, `city` (R14) | [DOK] / VARSAYIM liste ve ret |
| REQ-CNT-04 | SMS ve email içeriği ekrana stream edilir | [DOK] |
| REQ-APR-01 | Pazarlamacı önizler, düzenler, onaylar; onaysız gönderim yok | [DOK] |
| REQ-APR-02 | Düzenlenen içerik backend'de LLM çıktısıyla aynı doğrulayıcıdan geçer | [DOK] çıkarımı (düzeltme #5) |
| REQ-APR-03 | Onay içeriğin son sürümüne bağlıdır; onay sonrası düzenleme yeniden onay ister | VARSAYIM (düzeltme #5) |
| REQ-SND-01 | Gönderim asenkron kuyruk üzerinden | [DOK] |
| REQ-SND-02 | Kanal başına ≤100 msg/sn; sistem genelinde, worker sayısından bağımsız, retry'lar dahil | [DOK] çıkarımı (düzeltme #7, R6) |
| REQ-SND-03 | Aynı kişiye aynı mesaj iki kez gitmez | [DOK] |
| REQ-SND-04 | Retry stratejisi (0.4) | [DOK] / VARSAYIM tasarım |
| REQ-TRK-01 | Canlı gönderim durumu (dashboard) | [DOK] |
| REQ-TRK-02 | Metrics endpoint'i | [DOK] |
| REQ-TRK-03 | Health check endpoint'i | [DOK] |
| REQ-AI-01 | Claude çıktısı JSON; backend'de şemayla kontrol | [DOK] |
| REQ-AI-02 | Testler gerçek key olmadan, mock LLM ile | [DOK] |
| REQ-AI-03 | Key yoksa uygulama mock LLM ile çalışır, arayüzde belirtilir | VARSAYIM (düzeltme #3) |
| REQ-MOCK-01 | Mock provider zaman zaman hata, gecikme, 429 döndürür; oranlar ayarlanabilir | [DOK] / VARSAYIM ayar |
| REQ-MOCK-02 | Mock provider kendi rate limitini uygular (`Retry-After` ile 429) ve `Idempotency-Key` ile tekilleştirir | VARSAYIM (düzeltme #8) |
| REQ-DLV-01 | `docker compose up` tek komutla kurulum (seed + mock provider dahil) | [DOK] |
| REQ-DLV-02 | `make test` ile testler çalışır [DOK]; yalnızca Docker + make gerektirir | [DOK] / VARSAYIM (düzeltme #2, R3) |
| REQ-DLV-03 | Tüm Claude session export'ları (Claude Code `/export` ve claude.ai paylaşım linkleri) temizlenmiş halde `ai-sessions/`'da | [DOK] (düzeltme #4, R13) |

Her test fonksiyonunun üstünde `// REQ-XXX-NN`. CI script'i testi olmayan REQ ID'lerini listeler; P6'da liste boş olmalı.

### 4.2 Karakter limiti ve kişiselleştirme (düzeltme #6)

- "Karakter" = Unicode rune sayısı (VARSAYIM). GSM-7/UCS-2 farkı (Türkçe karakterlerle gerçek SMS segmenti 70'e düşer) `DECISIONS.md`'de not edilir; arayüzde bilgi olarak segment sayısı gösterilir.
- **Onay anında:** şablon segmentteki **en uzun `first_name`** (boşsa fallback değeri) ile render edilir; 160/80 aşılırsa onay engellenir.
- **Gönderim anında:** her alıcı için render sonrası ikinci kontrol; aşan teslimat `failed` (gönderilmez) ve dashboard'da görünür.

### 4.3 `make test` sözleşmesi (düzeltme #2)

- `make test` = `docker compose -f compose.test.yml run --rm --build test`; test imajı Go + Node + araçları içerir; Postgres geçici ayağa kalkar, bitince `down -v`.
- Konteynerde `ANTHROPIC_API_KEY` tanımsız, `LLM_PROVIDER=mock`. Gerçek istemci test binary'sinde kurulursa panik (guard).
- Kapsadığı katmanlar: Go unit + entegrasyon + API E2E + Vitest.
- Yardımcı hedefler: `make test-unit` (host'ta Go, DB yok), `make test-integration`, `make test-e2e`, `make test-web`.
- Tek test: `go test ./internal/delivery -run 'TestRetryPolicy_NextDelay/429_honors_Retry-After' -count=1`.
- CI, `make test` hedefini GitHub runner'ında (Docker + make) çalıştırır; yerelde ve CI'da aynı compose dosyası kullanılır.
- **Doğrulama:** CI'da yalnızca Docker bulunan bir job `make test` çalıştırır; `ANTHROPIC_API_KEY` tanımsızken geçtiği ayrıca kontrol edilir.

### 4.4 Test katmanları

| Katman | Senaryolar |
|---|---|
| **Unit** | SMS 160 kabul / 161 ret; konu 80/81; render sonrası aşım (en uzun isim); izinsiz değişken (`{{password}}`); boş `first_name` fallback; segment kuralında izinsiz alan/operatör, aşırı iç içe yapı; injection içeren değerlerin yalnızca parametre kalması; retry politikası (429 + `Retry-After`, 5xx backoff, 4xx retry yok, max deneme); idempotency anahtarı determinizmi |
| **LLM (mock)** | Geçerli JSON kabul; bozuk JSON ve şema ihlali ret (tek düzeltme denemesi, sonra anlamlı hata); limit dışı içerik; prompt injection içeren brief'te şema dışı çıktı reddi; stream'in parça parça gelmesi ve yarıda kesilmesi; SSE olay sırası (`delta…` → `validated` / `invalid`) ve `validated` gelmeden onayın reddi |
| **LLM fabrikası** | key yok + `auto` → mock; key var + `auto` → gerçek istemci (yalnızca fabrika seviyesinde, ağ yok); `real` + key yok → açılış hatası; loglarda key yok |
| **Entegrasyon (DB)** | Örnek brief segmenti manifestteki sayıyı verir; izinsizler hariç; aynı teslimatın ikinci kez kuyruğa girmesi unique constraint ile engellenir; eşzamanlı worker'lar aynı işi almaz |
| **Gönderim** | Senaryo betikli provider (`httptest`): 429, 500, gecikme, "kabul edildi ama yanıt kayboldu"; iki kez gönderilmeme (DB + `Idempotency-Key`); **birden fazla worker ile** kanal başına ≤100/sn (sahte saat); retry'ların limite dahil olması; 429'ların ayrı bütçesi |
| **Gönderim — gerçek mock provider** (R10) | `cmd/mockprovider` sabit seed'li hata/gecikme/429 oranlarıyla ayağa kaldırılır; gerçek worker ile tam kampanya gönderilir: her alıcıya en fazla bir başarılı teslimat, kanal başına gözlenen hız ≤100/sn, tüm teslimatlar `sent` veya `failed` ile sonuçlanır |
| **Onay** | Düzenlenmiş SMS 161 → 422; düzenlemeye izinsiz değişken → ret; onay sonrası düzenleme → durum `content_ready`, gönderim isteği 409; frontend atlanarak gönderilen geçersiz içerik ret |
| **API E2E** | Key'siz: brief → segment → içerik (mock stream) → onay → gönder → `completed`; onaysız gönderim 409; `/healthz`, `/metrics`, `/api/config` (aktif provider) cevap verir |
| **Frontend** | Karakter sayacı; limit aşılınca onay pasif; stream metni artımlı görünür; LLM içeriği HTML olarak yorumlanmaz; "Mock LLM" rozeti |

### 4.5 Unit test stili ve kuralları

**İsimlendirme:** `Test<Birim>_<Davranış>`; alt test adı cümle (`"161 chars is rejected"`); kara kutu için `package x_test`.

**Table-driven (varsayılan):** `[]struct{ name; in; want; wantErr }` + `t.Run(tc.name, ...)`; hatalar `errors.Is` + sözleşmedeki sentinel hatalarla; stdlib + `go-cmp` (VARSAYIM); `t.Parallel()` paylaşılan durum yoksa; yardımcılarda `t.Helper()`.

**Determinizm:**

| Kaynak | Kural |
|---|---|
| Zaman | `time.Now()` doğrudan yok, `Clock` enjekte; testlerde sahte saat + sabit referans (`2026-01-15T12:00:00Z`); `time.Sleep` yasak |
| Rastgelelik | Global `math/rand` yasak; seed'li `*rand.Rand` enjekte |
| Ağ | Yalnızca `httptest`; dış host'a giden isteği düşüren guard transport |
| DB | Unit'te yok; entegrasyonda test başına transaction-rollback veya ayrı şema; `-shuffle=on` |
| LLM | Yalnızca `llm.Mock` (fixture tabanlı, deterministik stream sırası) |
| Eşzamanlılık | `-race`; goroutine sızıntısı için `goleak` (VARSAYIM) |
| Çalıştırma | CI'da `-count=1 -shuffle=on -race`; flaky test karantinaya alınmaz, düzeltilir |

**Test dosyası düzeni:** dosya adı özellik/REQ alanına göre verilir, review turuna göre asla (`round2`, `fix`, `review` ekleri yasak; `scripts/hooks/check-test-files.sh` pre-commit'te reddeder). Test adları ve `// REQ-…` yorumları REQ ID'sini taşır ki grep bulsun; yorum `docs/requirements.md`'deki bir kabul ölçütüne çözülmezse test reddedilir (§3.1 madde 7). Review bulguları mevcut dosyada yeni table-driven satır veya yeni alt test olur. Yeni dosya yalnızca yeni bir test katmanı ya da henüz dosyası olmayan yeni bir alan için açılır ve `docs/requirements.md` §İzlenebilirlik tablosuna yazılır.

**Kapsam:** domain paketleri için %80 satır (VARSAYIM); asıl ölçü REQ eşlemesinin tam olması. **Fixture:** `testdata/`; golden dosyaları yalnızca test yazarı `-update` ile günceller.

---

## 5. Seed verisi (P2)

### 5.1 İlkeler

- **Deterministik:** `cmd/seed --seed=42 --users=5000 --ref-time=<RFC3339>`; aynı parametre → aynı veri. 5000 VARSAYIM ([DOK] "birkaç bin").
- **Referans zaman:** compose'da `ref-time=now` (demo'da "son 30 gün" anlamlı), testlerde sabit.
- **Idempotent:** tekrar çalıştırmak çoğaltmaz.
- **Manifest:** `testdata/seed/manifest.json` beklenen sayıları tutar (örn. "İstanbul ∧ son 30 günde sepete ekleme ∧ satın alma yok ∧ SMS izni = N"); değişiklik PR'da diff olarak görünür.
- **Gerçek olmayan veri:** email yalnızca RFC 2606 alan adları (`example.com`, `example.test`); telefon açıkça kurgusal blok `+90 500 000 xx xx` (VARSAYIM — resmî kurgusal TR aralığı bilinmiyor, mock provider dışarı göndermediği için risk düşük); isimler küratörlü listeden.

### 5.2 Alanlar

**`customers`**

| Alan | Neden |
|---|---|
| `id` (deterministik UUID) | Kimlik, idempotency anahtarı |
| `first_name`, `last_name` | `{{first_name}}` [DOK]; bir kısmı boş (fallback), bir kısmı çok uzun (160 sınırını zorlamak) |
| `email` | Email kanalı |
| `phone` (E.164) | SMS kanalı; bir kısmında yok |
| `city` (İstanbul ~%30, diğer büyük şehirler) | Örnek brief "İstanbul'daki" [DOK] |
| `language` (`tr`/`en`) | Bonus: dile göre içerik |
| `sms_opt_in`, `email_opt_in` + `*_opt_in_at` | Kanal izni [DOK]; ikisi / biri / hiçbiri dağılımı |
| `created_at`, `last_seen_at` | Segment genişletmeleri |
| `loyalty_tier` (VARSAYIM) | Farklı brief denemeleri |

**`events`:** `customer_id`, `type` (`cart_add`, `purchase`, `page_view`), `occurred_at`, `amount`, `product_category`.

**`customer_stats`** (segment DSL'i yalnızca bu alanlara izin verir): `last_cart_add_at`, `last_purchase_at`, `orders_30d`, `cart_adds_30d`, `total_spent`, `total_orders`.

**Bilerek yok:** TC kimlik, doğum tarihi, cinsiyet, adres, ödeme bilgisi — veri minimizasyonu.

### 5.3 Bilinçli kenar durumları

- Örnek brief'e tam uyan, sayısı bilinen grup.
- Sepete ekleme 29 / 30 / 31 gün önce (sınır).
- Sepete ekleyip satın almış olanlar (dışlanmalı).
- İstanbul'da olup izni olmayanlar (dışlanmalı).
- Aynı telefon/email'i paylaşan iki kayıt — tekilleştirme müşteri bazında (VARSAYIM; adres bazı `DECISIONS.md`'de tartışılır).
- Türkçe karakterli ve çok uzun isimler.

---

## 6. Güvenlik

| # | Önlem | Doğrulama |
|---|---|---|
| 1 | Key yalnızca `.env`'de [DOK]; `.env.example`'da `ANTHROPIC_API_KEY=` boş, `LLM_PROVIDER=auto` | `git check-ignore -v .env`; CI: `git ls-files` içinde `.env` yok; `.env.example`'da `sk-ant-` yok |
| 2 | Sır taraması: commit, push, CI tüm geçmiş | Hook'ta `gitleaks protect --staged`, CI'da `gitleaks detect`; sahte key ile negatif test |
| 3 | Key loglanmaz: config `String()`/`LogValue()` maskeler; slog redaction; provider seçimi loglanır, değer loglanmaz | Unit test: config logu `sk-ant` içermez; E2E log çıktısında `sk-ant` sayısı 0 |
| 4 | Key frontend'e ulaşmaz; yalnızca backend Claude'u çağırır; Vite'a yalnızca `VITE_` | CI: build sonrası `web/dist` içinde `sk-ant`/`ANTHROPIC` yok |
| 5 | Key image'a girmez: runtime `env_file`, build `ARG` değil; `.dockerignore` `.env`'i dışlar | CI: `docker history --no-trunc` ve image dosya sisteminde arama |
| 6 | CI key'siz çalışır; GitHub Actions secret'ı tanımlanmaz | CI grep: pipeline dosyasında `ANTHROPIC_API_KEY` referansı yok; test guard |
| 7 | `ai-sessions/` temizliği [DOK] (bkz. 7. bölüm) | `make sanitize-sessions` + gitleaks + özel regex (hook ve CI) + commit öncesi elle göz atma |
| 8 | LLM çıktısı asla SQL değil [DOK]: DSL allowlist (alan, operatör, tip, derinlik, eleman sayısı) → parametreli sorgu (pgx/sqlc) | Injection payload unit testleri; gosec G201/G202; reviewer listesi |
| 9 | En az yetkili DB rolü: uygulama DDL çalıştıramaz, migration ayrı rol | Entegrasyon testi: uygulama rolüyle `DROP TABLE` yetki hatası |
| 10 | Brief güvenilmeyen girdi: sistem prompt'u ile girdi ayrı; çıktı her zaman şemayla doğrulanır; yalnızca izinli değişkenler | Injection fixture'ı → şema dışı çıktı ret |
| 11 | Düzenlenen içerik de aynı doğrulayıcıdan geçer (REQ-APR-02) | 4.4 onay testleri |
| 12 | XSS: `v-html` yok; email önizlemesi sandbox'lı `iframe`; backend `html/template` | ESLint `vue/no-v-html` error; `<script>` içeren çıktıyla bileşen testi |
| 13 | Maliyet/kötüye kullanım: brief max uzunluk, body limiti, `max_tokens`, Claude timeout, LLM endpoint rate limit | Uzun brief 413/422; timeout'ta anlamlı hata; limitte 429 |
| 14 | Loglarda PII yok: müşteri ID'si, telefon/email değil | Teslimat log testi: `@example.com`, `+90` deseni yok |
| 15 | Ağ izolasyonu: Postgres ve mock provider yalnızca iç ağda | `docker compose config` çıktısında db için `ports:` yok (CI'dan çıkarıldı 2026-10-27; elle/yerelde kontrol) |
| 16 | Bağımlılıklar: `govulncheck`, `npm audit --audit-level=high`; Go sürümü `go.mod`'da, imajlar digest ile sabit | CI; kritik bulgu pipeline'ı kırar |
| 17 | SAST: golangci-lint içinde gosec; CodeQL (public repoda ücretsiz) P2'de eklenir | CI lint ve CodeQL job'ları |
| 18 | Auth yok (VARSAYIM, yerel demo); `/metrics` yalnızca iç ağ | README + `DECISIONS.md`; compose port incelemesi |
| 19 | Repo public: her commit herkese açık, bu yüzden 1–7 istisnasız uygulanır; commit e-postası noreply | GitHub secret scanning + push protection açık; `git log --format=%ae` yalnızca noreply adresi |
| 20 | Reviewer güvenlik listesi: her PR'da 1–19 (16–19 CI ve repo ayarlarıyla doğrulandığı için reviewer yalnızca CI sonucunu ve ayarları teyit eder) | PR'daki raporda işaretli liste (R9) |

---

## 7. AI session export'ları (düzeltme #4)

- **Kural:** her Claude Code session'ı kapanmadan `/export` — ana, worktree ve test yazarı session'ları dahil, istisnasız.
- **İsimlendirme:** `ai-sessions/YYYY-MM-DD_p<faz>_<rol>_<konu>.md` (örn. `2026-10-26_p0_planning_case-analysis.md`).
- **Subagent'lar:** ana export'ta yalnızca özet olarak görünebilir; izolasyonu önemli roller (test yazarı) bu yüzden **ayrı session'da** çalışır ve ayrı export edilir — rol ayrımının kanıtı da olur.
- **claude.ai kullanılırsa:** doküman "claude.ai'da paylaşım linki" de istiyor; link `INDEX.md`'ye satır olarak eklenir (R13).
- **`ai-sessions/INDEX.md`:** tarih · faz · rol · konu · dosya veya link.
- **Temizlik:** `make sanitize-sessions` (key desenleri, `/Users/<kullanıcı>` yolları, kişisel email maskelenir; ad-soyad için `SANITIZE_EXTRA_PHRASES="Ad Soyad,…"`) → gitleaks + özel regex → elle göz atma.
- **Kritik:** bu planlama session'ı git'ten önce yapıldı; **P0'ın ilk işi** export'udur.

---

## 8. Teslim kontrol listesi

- [ ] GitHub public repo; README'deki kurulum adımları temiz bir klondan doğrulandı
- [ ] `docker compose up` tek komut, **key'siz** çalışır (web arayüzü + api + worker + seed + mock provider + mock LLM) (R12)
- [ ] `make test` yalnızca Docker + make ile, key'siz
- [ ] `README.md`: kurulum, mimari özeti, bonus olarak CI, yetiştirilemeyenler (yapılmayan bonuslar dahil: A/B, dile göre içerik, zamanlanmış gönderim)
- [ ] `DECISIONS.md`: bu plandaki tüm VARSAYIM'lar ve gerekçeleri
- [ ] `ai-sessions/` + `INDEX.md`: tüm session'lar, temizlenmiş
- [ ] Kişisel bilgi yok: izlenen dosyalarda ve session export'larında ad-soyad, kişisel e-posta ve yerel bilgisayar yolları temizlendi; squash merge commit'leri noreply adresiyle yazılıyor (geçmiş commit'ler yeniden yazılmaz, D58)
- [ ] REQ eşleme raporu: testi olmayan REQ yok
- [ ] P7 yapıldıysa: `p7-done` etiketi, REQ-SCH testleri yeşil, `README.md` bonuslarda CI'ın yanında zamanlanmış gönderimi anar (§9.6); yapılmadıysa P7a dalı `main`'e girmemiştir

---

## 9. P7 — Bonus fazı

> **Kaynak:** case dokümanının "Bonus (isteğe bağlı)" bölümü: zamanlanmış gönderim, A/B varyantları, kullanıcının diline göre içerik, CI pipeline. Bonus yalnızca çekirdek akış sağlamsa yapılır [DOK]; P6 kapısı (`p6-done`) P7'nin ön koşuludur.
> **Kapsam [SEN]:** P7a zamanlanmış gönderim (yapılacak) ve P7b CI pipeline (yapıldı). **A/B varyantları ve dile göre içerik kapsam dışıdır**; sahip eklerse ayrı kırım ve ayrı REQ alanı alır (`language` seed alanı §5.2'de hazır bekler).

### 9.1 P7b — CI pipeline ✅ (yapıldı, P0–P6)

Yeni iş yok; plan bonusun karşılandığını açıkça göstermek için kırımı listeler.

| Ne var | Nerede |
|---|---|
| GitHub Actions iş akışı, her PR'da ve `main`'e push'ta | `.github/workflows/ci.yml` — job'lar: `detect`, `pr-policy`, `repo-policy`, `go-lint`, `api-contract`, `web`, `sql-lint`, `db-roundtrip`, `hook-tests`, `make-test`, `gitleaks`, `ai-sessions-scan`, `govulncheck`, kapı job'ı `ci-ok` |
| Sunucu tarafı zorlama | `main` ruleset'i: PR + `ci-ok` zorunlu, branch güncel, force push/silme yasak, bypass yok (§1.6) |
| Ajan merge kuralı | `scripts/claude/guard-git.sh`: `gh pr merge` yalnız açık PR numarası + `--squash` ve `ci-ok` yeşilken (§2.3) |
| Yalnız doküman PR'ında hafif yol | `detect` → `heavy=false`; politika ve sır job'ları her zaman koşar (§1.2, D51) |
| Bilinçli çıkarılan/ertelenen kontroller | `web-image`, `compose-ports`, `actionlint` CI'dan çıkarıldı (D27 güncellemesi, D51); CodeQL ve imaj dosya sistemi taraması eklenmedi (D27). P7 bunları geri getirmez |

**Kapı:** son `main` merge'lerinde `ci-ok` yeşil; `make test` CI'da key'siz koşuyor (§4.3). P7a yeni bir CI job'ı gerektirmez: migration `db-roundtrip`, sözleşme `api-contract`, testler `make-test` ile zaten koşar.

### 9.2 P7a — Zamanlanmış gönderim: tasarım

**Hedef:** Onaylı kampanya "şimdi" yerine ileri bir zamana planlanabilir; o zaman gelince gönderim, bugünkü `POST /send` ile **birebir aynı** transaction'la başlar. Kuyruk, hız sınırı, retry ve idempotency değişmez.

**Seçenekler (karar D62 olarak `DECISIONS.md`'ye yazılır):**

| | A — Teslimatlar planlamada doğar, `next_attempt_at = scheduled_at` | **B — Plan kampanyada durur, teslimatlar zamanı gelince doğar (seçilen)** |
|---|---|---|
| Kitle ve izin | Planlama anında dondurulur; sonradan izin veren eklenmez | Gönderim anında okunur, anlık gönderimle aynı anlam |
| İptal / yeniden planlama | Kuyruktan satır silmek veya hepsinin `next_attempt_at`'ini güncellemek (binlerce satır) | Tek satır güncellemesi |
| `scheduled → sending` geçişini kim yapar | Claim sıcak yolu kampanya satırını kilitlemek zorunda kalır, ya da yine ayrı bir döngü gerekir | Ayrı, küçük bir döngü; claim'e dokunulmaz |
| Yeni kod | Yok gibi görünür ama durum geçişi ve iptal karmaşıklaşır | Bir döngü + bir paylaşılan fonksiyon |

A, §0.3'te ilk düşünülen yoldu; B'nin seçilme nedeni gönderim anlamının "şimdi gönder" ile aynı kalması ve iptalin kuyruğa dokunmamasıdır.

**Veri modeli — migration `0008_campaign_schedule` (up + down):**
- `campaigns.scheduled_at timestamptz NULL`. Durum kümesine `scheduled` eklenir (`campaigns_state_check`).
- Kısıtlar: `state = 'scheduled'` ⇒ `scheduled_at IS NOT NULL`; `draft…approved` ⇒ `scheduled_at IS NULL`; `sending`/`completed`'da planlı başlamışsa dolu kalır (geçmiş bilgisi: "planlanan 10:00, başladı 10:00:01"). `campaigns_approval_present_check` `scheduled`'ı da kapsar.
- Durum tetikleyicisi (`campaigns_state_transition`, `CREATE OR REPLACE`): yeni geçişler `approved → scheduled`, `scheduled → approved` (iptal), `scheduled → sending` (zamanı geldi veya "şimdi gönder").
- Kısmi indeks: `campaigns (scheduled_at) WHERE state = 'scheduled'` — döngünün sorgusu yalnız bunu tarar.
- Down: önce `scheduled` kampanyalar `approved`'a döndürülür (yeni tetikleyici buna izin verir), sonra eski tetikleyici fonksiyonu ve kısıtlar geri yüklenir, kolon `-- allow-destructive` ile düşer. `make db-roundtrip` hedefi zaten var (P2); pre-push ve CI `db-roundtrip` job'ı yeni migration'ı `up → down → up` koşar, şema diff'i boş olmalı. Migration değişmezliği gereği 0003 düzenlenmez.

**Durum makinesi (D4 genişler):**

```
approved ──PUT /schedule──▶ scheduled ──zamanı geldi (worker) / POST /send──▶ sending ──▶ completed
    ▲                           │  ▲
    └──────DELETE /schedule─────┘  └── PUT /schedule (yeniden planlama, durum aynı)
```

- `scheduled`'da düzenleme, segment/içerik üretimi ve onay **409 `invalid_state`** (VARSAYIM). Pazarlamacı önce iptal eder (`approved`'a döner), sonra bugünkü kurallar geçerlidir: düzenleme onayı düşürür (REQ-APR-03). Gerekçe: planı sessizce düşüren bir düzenleme, "planlandı" sanılan kampanyanın hiç gitmemesine yol açar; açık iptal savunulabilir. (Alternatif: düzenleme planı da düşürür — açık soru 2.)
- Önizleme (`preview`) `scheduled`'da serbesttir.
- **Üretim kilidi (D41, D52):** `PUT /schedule` gönderimle aynı denetimi kendi satır kilitli transaction'ında yapar (`LockedCampaign.CheckNoGeneration` → 409 `generation_in_progress`). `scheduled`'da üretim başlatılamadığı için (denetim sırası `invalid_state` › `generation_in_progress`) zamanı gelen kampanyada kira bulunamaz; döngü yine de aynı denetimi savunma amaçlı çağırır. Planlama ile üretim başlangıcı aynı anda gelirse satır kilidi yüzünden yalnız biri kabul edilir.

**API (`docs/contracts/openapi.yaml`):**
- `PUT /api/campaigns/{campaign_id}/schedule`, gövde `{"content_version_id", "scheduled_at"}` → **200** `{"campaign_id", "state": "scheduled", "content_version_id", "scheduled_at"}`. `approved`'da planlar, `scheduled`'da yeniden planlar. Teslimat oluşturmaz.
- `DELETE /api/campaigns/{campaign_id}/schedule` → **200** `{"campaign_id", "state": "approved", "scheduled_at": null}`; `scheduled` dışında 409 `invalid_state` (gönderim başlamışsa iptal yok).
- `POST /send` artık `approved` **veya** `scheduled`'da kabul edilir ("şimdi gönder" planı aynı transaction'da düşürür; iki istekli iptal+gönder yarışı yok). Diğer davranışı ve 202 gövdesi değişmez.
- `Campaign` ve `CampaignStatus` şemalarına `scheduled_at` (nullable, UTC `Z`) eklenir; `CampaignState` enum'una `scheduled`. Bu bir yanıt enum'u eklemesidir (`oasdiff` uyarısı, kırıcı değil; D41'deki gibi).
- **Doğrulama (422 `validation_failed`, yol `scheduled_at`):** RFC 3339, **ofset zorunlu** (`Z` veya `+03:00`; ofsetsiz yerel saat `invalid_value`); `scheduled_at` ≥ şimdi + `SCHEDULE_MIN_LEAD` (varsayılan 1 dk, alan kodu `schedule_too_soon`); ≤ şimdi + 30 gün (`schedule_too_far`). Saklama ve yanıt UTC. "Şimdi" enjekte `clock.Clock`'tur.
- **Denetim sırası** (gönderimle aynı kalıp, D42): biçimsiz path 404 › gövde 400/413/422 › 404 `campaign_not_found` › 404 `content_version_not_found` › 409 `generation_in_progress` › 409 `invalid_state` › 409 `stale_content_version`.
- Seçenek olarak `POST /send`'e isteğe bağlı `scheduled_at` alanı da düşünüldü; reddedilme nedeni: `/send`'in "202 + teslimat sayıları" anlamı bozulur ve iptal/yeniden planlama için yine ayrı uç gerekir (açık soru 1).

**Saat dilimi (VARSAYIM):** API yalnız mutlak zaman alır (ofsetli RFC 3339), veritabanı `timestamptz`, yanıtlar UTC. Arayüz zamanı **Europe/Istanbul** olarak girer ve gösterir (etiket "TSİ"), tarayıcının saat diliminden bağımsız; dönüşüm `Intl` ile IANA bölgesi üzerinden yapılır (Türkiye 2016'dan beri sabit UTC+3, yine de sabit ofset yazılmaz).

**Worker davranışı (`cmd/worker`):**
- Yeni döngü `campaign.Scheduler` (dispatcher'lardan ayrı goroutine): her `SCHEDULER_INTERVAL`'de (varsayılan 1 sn) bir transaction açar, `SELECT … FROM campaigns WHERE state = 'scheduled' AND scheduled_at <= $now ORDER BY scheduled_at LIMIT 10 FOR UPDATE SKIP LOCKED` ile zamanı gelenleri kilitler ve her birini **kendi transaction'ında** başlatır (biri hata verirse diğerleri bekletilmez).
- Başlatma, `CreateDeliveries`'in gövdesinden çıkarılan paylaşılan fonksiyondur (`startSendingLocked`): aynı segment derlemesi (kampanyanın saklı referans zamanıyla), aynı izin filtresi, aynı `INSERT … UNIQUE(campaign_id, channel, customer_id)`, `state = 'sending'`, `sending_started_at = now`, kitle boşsa aynı transaction'da `completed`. API'nin `Send`'i ve döngü bu tek fonksiyonu çağırır; anlam ayrışamaz.
- **Meşgul yoklama yok:** sorgu yalnız kısmi indeksi tarar ve iş yoksa hiçbir satır kilitlemez; dispatcher zaten 200 ms'de bir yokluyor (D5), 1 sn'lik indeksli sorgu ek yük değildir. `LISTEN/NOTIFY` reddedildi: ayrı bağlantı ve yeniden bağlanma mantığı ister, dakika hassasiyetli bir plan için kazancı yok (VARSAYIM). Hassasiyet: planlanan an + ≤ 1 sn + ilk partinin claim süresi.
- **Çoklu worker:** advisory lock gerekmez; `SKIP LOCKED` + kilit altında durum yeniden denetimi aynı kampanyanın iki kez başlatılmasını imkânsız kılar (ikinci transaction `sending` görür, atlar). Yine de olsa `UNIQUE` kısıtı ikinci teslimat setini reddeder.
- **Yeniden başlatma güvenliği:** bellekte zamanlayıcı yok; tek doğruluk kaynağı satırdır. Başlatma transaction'ı commit edilmeden çöken worker hiçbir iz bırakmaz, satır `scheduled` kalır, sonraki tur başlatır. Worker kapalıyken zamanı geçen kampanya, worker açılınca **geç de olsa** başlatılır (azami gecikme sınırı yok, VARSAYIM; açık soru 3); gecikme (`sending_started_at − scheduled_at`) loglanır.
- **İptal–başlatma yarışı:** ikisi de kampanya satır kilidini alır; önce commit eden kazanır. Döngü önce kazanırsa `DELETE /schedule` 409 `invalid_state` alır ("gönderim başladı"); iptal önce kazanırsa döngü satırı `approved` görür ve atlar.
- **Değişmeyenler:** idempotency anahtarı ve `Idempotency-Key` (= teslimat id), kanal başına ≤ 100/sn (aynı anda zamanı gelen birçok kampanya yalnız kuyruğu uzatır), retry ve 429 bütçesi (`first_attempt_at` ilk claim'de dolar, planlanan bekleme bütçeyi tüketmez), claim'deki izin yeniden okuması (REQ-AUD-04).
- **Metrics (`docs/contracts/metrics.md`):** `scc_campaigns_scheduled` (gauge, `scheduled` durumundaki kampanya sayısı) ve `scc_scheduled_sends_started_total` (counter). Sağlık uçları değişmez.

**Arayüz (`docs/contracts/ui.md`, DESIGN.md):**
- **Onay ekranı:** `ConfirmDialog kind="send"` iki seçenek alır: "Şimdi gönder" (varsayılan, bugünkü davranış) ve "İleri tarihte gönder" (tarih + saat, TSİ). İstemci tarafı denetim API ile aynı sınırları gösterir (geçmiş/çok yakın/30 günden uzak), ama güvence API'dedir; 422 alan hatası diyalogda `field-error` olarak görünür. Başarıda `tracking` rotasına geçilir. Önerilen testid'ler: `confirm-dialog-send-now`, `confirm-dialog-send-later`, `confirm-dialog-scheduled-at` (input), onay etiketi kipine göre "Gönder" / "Planla".
- **Takip ekranı:** kampanya `scheduled` iken sayaçlar yerine plan paneli: `schedule-panel`, `schedule-time` (`data-value` = UTC ISO, metin TSİ), `schedule-countdown` (`data-value` = kalan saniye, 1 sn'de bir; sıfıra inince "Başlatılıyor…" ve akıştan `sending` gelince sayaçlara geçer), `schedule-cancel` (iptal → `approval` rotası), `schedule-send-now` (`ConfirmDialog` → `POST /send`). `delivery-counters` için `data-state="scheduled"` eklenir. Canlı akış bugünkü gibi açılır; durum değişimi `status` olayıyla gelir. Başlamış planlı kampanyada sayaçların üstünde "Planlanan: …" satırı kalır.
- **Onay ekranı, `scheduled` iken:** düzenleme ve onay pasiftir, açıklama "Planlı kampanyayı değiştirmek için planı iptal edin" + iptal düğmesi (ayrıntı ui.md'de).
- i18n metinleri tek dosyada (`web/src/i18n`), LLM içeriği dışında yeni serbest metin yok.

### 9.3 Gereksinimler (`docs/requirements.md`, yeni alan SCH)

Test yazarı yalnız `docs/requirements.md`'den çalıştığı için (D50) testte gerekecek her ad, yol, alan kodu ve ortam değişkeni REQ metnine yazılır. "Kapsam dışı ve bonus" paragrafı güncellenir (zamanlanmış gönderim artık REQ'li; A/B ve dil kapsam dışı). Taslak kabul ölçütleri:

- **REQ-SCH-01 — Gönderim ileri bir zamana planlanır · Faz P7 · [DOK] bonus + VARSAYIM tasarım:** `PUT …/schedule` istek/yanıt biçimi; `approved` → `scheduled`; teslimat oluşmaz (`deliveries.total` = 0, `status.counts.total` = 0); `scheduled_at` UTC döner, `GET /api/campaigns/{id}` ve `/status`'ta görünür; ofsetsiz zaman, `SCHEDULE_MIN_LEAD`'den yakın, 30 günden uzak → 422 ve alan kodları; tam denetim sırası; üretim sürerken 409 `generation_in_progress` (iki `campaign.NewService` örneğiyle, REQ-SND-01'deki kalıp).
- **REQ-SCH-02 — Zamanı gelince gönderim kendiliğinden başlar · Faz P7 · VARSAYIM:** `campaign.Scheduler`'ın tek turu (`RunOnce(ctx)`; sahte saat `scheduled_at`'in öncesinde → başlatmaz, sonrasında → başlatır); başlatılan kampanyada teslimat sayıları anlık gönderimle aynı kurala uyar (kanal kitlesi, izin, tekillik); `sending_started_at` ≥ `scheduled_at`; iki eşzamanlı `RunOnce` aynı kampanyayı bir kez başlatır; başlatma transaction'ı geri alınırsa kampanya `scheduled` kalır ve sonraki turda başlar; geçmişte kalmış plan ilk turda başlar.
- **REQ-SCH-03 — Plan iptal edilir, değiştirilir, öne alınır · Faz P7 · VARSAYIM:** `DELETE …/schedule` → `approved`, `scheduled_at` = `null`; `scheduled` dışında 409 `invalid_state`; `PUT` ile yeniden planlama; `scheduled`'da `POST /send` → 202 ve plan düşer; `scheduled`'da düzenleme, segment/içerik üretimi ve onay → 409 `invalid_state`, hiçbir sürüm/onay kaydı oluşmaz.
- **REQ-SCH-04 — Arayüz: planlama ve geri sayım · Faz P7 · VARSAYIM:** §9.2'deki testid'ler, TSİ gösterimi (test ortamının `TZ`'sinden bağımsız), geri sayım (`vi.useFakeTimers()`), iptal ve "şimdi gönder" akışı, `delivery-counters` `data-state="scheduled"`.

İzlenebilirlik tablosuna dört satır eklenir: SCH-01 `unit, integration, e2e`; SCH-02 `integration, e2e`; SCH-03 `integration, e2e`; SCH-04 `web-unit`.

### 9.4 Testler katmana göre

| Katman | Senaryolar |
|---|---|
| **Unit** | Zaman penceresi doğrulaması (sahte saat: sınırın 1 sn altı/üstü, 30 gün sınırı, ofsetsiz ve biçimsiz değer); denetim sırası; durum geçiş tablosu (`scheduled` giriş/çıkışları) |
| **Entegrasyon (DB)** | Tetikleyici yeni geçişlere izin verir, `scheduled → content_ready` gibi yasakları 23514 ile reddeder; kısıtlar (`scheduled` ⇒ `scheduled_at` dolu); `RunOnce` sahte saatle başlatır/başlatmaz; iki eşzamanlı `RunOnce` → tek teslimat seti; iptal–başlatma yarışında tek kazanan; geri alınan başlatma sonrası yeniden deneme; iki API örneğiyle üretim kilidi |
| **API E2E** | Key'siz: brief → segment → içerik → onay → `PUT /schedule` (yakın zaman; test yığınında `SCHEDULE_MIN_LEAD=0s`) → `scheduled` → worker başlatır → `completed`; iptal → `approved` → `POST /send` çalışır; 422/409 vakaları; yeni işlemler en az bir kez çağrılır (D30 `OPENAPI_VALIDATE=strict`) |
| **Gönderim — gerçek mock provider** | Değişmez; planlı kampanya başladıktan sonra aynı hız/tekillik iddiaları geçerli (ek senaryo gerekmez) |
| **Frontend (Vitest)** | Diyalog kip değişimi ve istemci denetimi; 422 alan hatası; TSİ biçimi; geri sayım ve sıfırda "Başlatılıyor…"; `status` olayıyla sayaçlara geçiş; iptal ve "şimdi gönder" |
| **Elle** | 375/768/1280 px'de Onay diyaloğu ve Takip plan paneli ekran görüntüsü PR'da (P5 responsive kuralı) |

### 9.5 Dilim akışı, sıra ve roller

P2–P6 ile aynı akış (§1.2, §3): sözleşme → test → kod; hiçbir rol kendi işini onaylamaz.

1. **Sözleşme** — `docs/p7-schedule-contracts` → `main` (mimar): `docs/requirements.md` (REQ-SCH-01..04, durum makinesi tablosu, HTTP 409 satırı, kapsam dışı paragrafı), `openapi.yaml`, `db-schema.md`, `go-interfaces.md` (`Service.Schedule`/`CancelSchedule`, `Scheduler.RunOnce`, store imzaları), `ui.md`, `metrics.md`, DESIGN.md'de plan paneli, `DECISIONS.md` D62 (+ bu bölümdeki VARSAYIM'lar). **Kabul:** `api-contract` yeşil; sahip REQ metnini onayladı.
2. **Dilim branch'i** — `slice/p7-schedule` güncel `main`'den.
3. **Testler** — `scripts/test-writer-worktree.sh origin/slice/p7-schedule test/p7-schedule`, ayrı session'da `claude --agent test-writer`; yalnız REQ-SCH kabul ölçütlerinden; eksik ad varsa "REQ eksik" döner (adım 1'e). **Kabul:** her REQ-SCH en az bir testle eşli, testler kırmızı, PR `slice`'a merge.
4. **Backend** — `feat/p7-schedule-backend`: migration 0008 (+down), `startSendingLocked` çıkarımı, servis + HTTP uçları, `campaign.Scheduler` + `cmd/worker` bağlantısı, config (`SCHEDULE_MIN_LEAD`, `SCHEDULER_INTERVAL`), metrics. **Kabul:** Go testleri yeşil, `make db-roundtrip` yeşil, mevcut REQ-SND testleri değişmeden yeşil.
5. **Arayüz** — `feat/p7-schedule-ui`: diyalog, Takip plan paneli, Onay ekranındaki `scheduled` görünümü, `api-types` yeniden üretimi. **Kabul:** Vitest yeşil, üç genişlikte ekran görüntüsü PR'da.
6. **Kapı** — `slice/p7-schedule` → `main`: reviewer (`code-reviewer`; migration ve yarışlar için `security-reviewer`/mimar bakışı) raporu ve doğrulayıcı (`verifier`) kanıtı PR'da; `ci-ok` yeşilse squash merge; `p7-done` etiketi; session export'ları `ai-sessions/`'a.

### 9.6 Faz kapısı (P7)

- §3.2 listesinin tamamı.
- REQ-SCH-01..04'ün her biri en az bir testle eşli ve yeşil; testsiz REQ listesi boş.
- `make db-roundtrip` yeşil (0008 `up → down → up`, şema diff'i boş); `make test` key'siz yeşil.
- Mevcut REQ-SND/REQ-TRK testleri değiştirilmeden yeşil (anlık gönderim anlamı korunuyor).
- Elle kanıt: compose yığınında 2 dk sonraya planlanan kampanya planlanan dakikada `sending`'e geçer; planlıyken `docker compose restart worker` sonrası yine başlar; iptal edilen kampanya hiç teslimat üretmez.
- `README.md` bonuslarda CI'ın yanında zamanlanmış gönderimi anar (README'yi o fazın sahibi düzenler); `DECISIONS.md` D62 güncel.

### 9.7 Riskler ve VARSAYIM'lar

| Konu | VARSAYIM / risk | Azaltma |
|---|---|---|
| Kitle anı | Gönderim anında izin ve iletişim bilgisi okunur, ama segmentin göreli pencereleri ("son 30 gün") **segment üretimindeki referans zamana** bağlı kalır (onaylanan kitle tanımı değişmez) | Arayüz planlama diyaloğunda kanal sayılarının gönderim anında değişebileceğini belirtir (açık soru 4) |
| Planlıyken değişiklik | Düzenleme/üretim/onay 409; önce iptal | Onay ekranında açıklama + iptal düğmesi |
| Geç başlatma | Worker kapalıyken kaçan plan açılınca başlatılır; sınır yok | Gecikme loglanır; sahip isterse azami gecikme eklenir (açık soru 3) |
| En kısa / en uzun ufuk | `SCHEDULE_MIN_LEAD` 1 dk, ufuk 30 gün | Ortam değişkeniyle ayarlanır; testte 0 |
| Saat dilimi | API ofsetli mutlak zaman; arayüz yalnız Europe/Istanbul | Vitest `TZ`'den bağımsız biçimi sınar |
| Saat kayması | Başlatma worker'ın saatine, doğrulama API'nin saatine dayanır (D52'deki sınırla aynı) | Tek makine/NTP; ≤ 1 sn hassasiyet iddia edilir |
| Durum makinesi değişikliği | Yeni durum mevcut ekranların `state` anahtarlı dallarını kırabilir | `scheduled` için her sayfada açık dal; mevcut testler değiştirilmeden yeşil kalmalı (kapı) |
| Uzun süre açık SSE | Planlı kampanyanın Takip sayfası günlerce açık kalabilir | Bugünkü 1 sn yoklama + 15 sn keepalive yeter; sekme kapanınca akış kapanır |

**Sahibe açık sorular (P7'nin ilk işi, 2026-10-28'de yanıtlanmadı):** P7 hangi session'da başlarsa başlasın, sözleşme PR'ından önce bu beş soru sahibe sorulur; yanıtlar ve gerekçeleri `DECISIONS.md` D62'ye yazılır, sonra §9.2–9.3 yanıtlara göre güncellenir. Parantez içindeki öneri, sahip aksini söylemezse uygulanacak varsayılandır.
1. Ayrı `PUT/DELETE …/schedule` uçları mı (öneri), yoksa `POST /send`'e isteğe bağlı `scheduled_at` mı?
2. `scheduled`'da düzenleme 409 mu (öneri), yoksa düzenleme planı sessizce düşürüp `content_ready`'e mi döner?
3. Worker kapalıyken kaçan plan: geç de olsa gönderilsin mi (öneri), yoksa belirli bir gecikmeden (ör. 1 sa) sonra gönderilmeyip işaretlensin mi?
4. Göreli segment pencereleri planlanan ana göre yeniden mi hesaplansın? Öneri: hayır (onaylanan kitle tanımı değişmesin).
5. Ufuk sınırları (en az 1 dk, en çok 30 gün) ve yalnız TSİ arayüzü uygun mu?

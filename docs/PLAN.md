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
- **Kampanya durum makinesi (VARSAYIM):** `draft → audience_ready → content_ready → approved → sending → completed` (kampanya düzeyinde `failed` yok; teslimat hataları `failed` teslimat olarak sayılır, kampanya yine `completed` olur; ayrıntı ve geri düşmeler `docs/requirements.md`). Onaysız `sending` yok [DOK]; onaydan sonra içerik düzenlenirse `content_ready`'e geri düşer (REQ-APR-03).

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
| Bonus: zamanlanmış gönderim | `next_attempt_at = scheduled_at` | Ayrı mekanizma |

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
| 7 | Tampon / bonus | Bonus yalnızca P6 tamamsa (zamanlanmış gönderim en ucuzu) | — |

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
- **Temizlik:** `make sanitize-sessions` (key desenleri, `/Users/<kullanıcı>` yolları, kişisel email maskelenir) → gitleaks + özel regex → elle göz atma.
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

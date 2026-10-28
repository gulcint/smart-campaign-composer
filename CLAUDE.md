# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Kaynak doküman

Tüm gereksinimlerin tek doğruluk kaynağı: `/Users/gulcintas/Desktop/SmartCampaignComposer/Smart Campaign Composer Case.html` (Insider One · AI Developer Case Study · 2026). Bu dosyayla çelişen bir karar verilecekse önce oraya bak; bilinçli sapmaları `DECISIONS.md`'ye yaz.

Bir konuda kararsız kalınırsa (`DECISIONS.md`, `docs/PLAN.md` ve sözleşmeler çelişiyor ya da sessizse) karar case dokümanı okunarak verilir; `DECISIONS.md` önceki kararların kaydıdır, case'in yerine geçmez. Case'e dayanan karar gerekçesiyle `DECISIONS.md`'ye yazılır (sahip kararı, 2026-10-28).

## Vizyon

Pazarlamacı yalnızca ne istediğini doğal dille yazar; Claude kitleyi ve kanal içeriklerini önerir; son kararı insan verir. Örnek brief:

> "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

Çekirdek akış (bunun uçtan uca sağlam çalışması her bonustan önce gelir):

1. **Kitle çıkarılır** — Claude brief'ten yapılandırılmış bir segment kuralı üretir; UI eşleşen kişi sayısını gösterir.
2. **Mesajlar üretilir** — SMS ve email içeriği UI'ya stream edilerek akar.
3. **İnsan onaylar** — önizleme, düzenleme, onay.
4. **Gönderilir** — kuyruk üzerinden mock provider'a.
5. **Canlı izlenir** — dashboard gönderim durumunu gösterir.

Gerçek kimseye mesaj gitmez: alıcılar seed edilmiş sahte kullanıcılar, gönderim bizim yazdığımız mock provider'a.

## Teknoloji

- **Frontend:** Vue.js
- **Backend:** Go
- **LLM:** Claude API (Anthropic Go SDK). Key `.env` içindeki `ANTHROPIC_API_KEY`'den okunur; repoda `.env.example` bulunur.
- **Veritabanı:** PostgreSQL.
- **Kuyruk:** PostgreSQL — `deliveries` tablosu kuyruk + durum + tekillik (`UNIQUE(campaign_id, channel, customer_id)`), `FOR UPDATE SKIP LOCKED`; kanal başına tek dispatcher (advisory lock) ile ≤100 msg/sn.
- **Repo:** GitHub, public, `gulcint/smart-campaign-composer` (GitLab hesap doğrulaması yapılamadığı için geçildi). Commit e-postası repoya özel GitHub noreply adresi. Commit'ler İngilizce, Conventional Commits. Case HTML `.gitignore`'da.
- **LLM sağlayıcı seçimi:** `LLM_PROVIDER=auto|real|mock` — key yoksa mock; `docker compose up` ve `make test` key'siz çalışır.

## Tasarım ve frontend

Görsel sistem ve ekran akışı: `/Users/gulcintas/Desktop/SmartCampaignComposer/DESIGN.md` (renk/tipografi token'ları, component envanteri, ekranlar, erişilebilirlik). Frontend'e dokunmadan önce okunur.
- Teknoloji: Vue 3 + Vite + TypeScript + Pinia; component'ler **shadcn-vue + Reka UI + Tailwind CSS v4**; ikonlar `@lucide/vue`; font Figtree + JetBrains Mono (self-host).
- Metin rengi: sahibin kararıyla **turuncu metin vurgu rengidir** (başlıklar, vurgular, anahtar sayılar, linkler); uzun gövde ve LLM metni koyu mürekkep kalır. Turuncuya yalnızca rol token'larıyla erişilir (`--text-accent*`), `--orange-*` doğrudan kullanılmaz.
- Arayüz dili Türkçe, metinler tek i18n dosyasında. LLM/düzenleme içeriği asla `v-html` ile basılmaz.
- Component adları ve `data-testid`'ler `docs/contracts/ui.md`'de tanımlanır (DESIGN.md tanımlamaz).

## Komutlar

Araçlar (Homebrew): `go`, `lefthook`, `gitleaks`, `golangci-lint`, `sqlfluff`, `actionlint`, `gh`; `go install` ile `goimports`, `govulncheck`.

- `make hooks` — git hook'larını (lefthook) ve commit şablonunu kurar. Klonladıktan sonra ilk iş.
- `make test` — tüm testler konteynerde, key'siz (`compose.test.yml` P2'de gelir; o zamana kadar bilerek hata verir).
- `make test-unit` — host'ta Go unit testleri, DB yok, mock LLM. Tek test: `go test ./internal/<pkg> -run 'TestX_Y/case' -count=1`.
- `make lint`, `make secrets` (tüm geçmişte gitleaks), `make sanitize-sessions`, `make check-sessions`.

Git kuralları (ayrıntı `docs/PLAN.md` §1–2):
- Branch: `<tür>/p<faz>-<kebab>` (`slice|test|feat|fix|refactor|docs|chore|ci|sec`). Commit: Conventional Commits, `feat`/`fix`/`test` için `Refs: REQ-XXX-NN` zorunlu.
- `main`'e doğrudan push yok; `main` ruleset'i PR ve `ci-ok` check'ini zorunlu tutar. Sahibin kararıyla (2026-10-26) ajan, `ci-ok` yeşilse PR'ı beklemeden merge eder: `gh pr merge <no> --squash --delete-branch` (kırmızı/bekleyen CI'da merge yok, onay beklenmez). `scripts/claude/guard-git.sh` (PreToolUse hook) ajanlar için `main`'e push, `--force`, `--no-verify` ve `--admin`'i engeller; `gh pr merge`'e ise yalnızca açık PR numarası + `--squash` ile ve o PR'da `ci-ok` yeşilken izin verir (durum okunamazsa engeller).
- Veri kaybettiren git komutları ajanlar için engellidir (`scripts/claude/guard-git.sh`): `git worktree remove --force`, `git branch -D`, `git clean -f`, `git reset --hard`, `git checkout -f|--`, `git switch -f`, `git restore` (yalnız `--staged` serbest), `git stash drop|clear`. Worktree kaldırmak için `scripts/worktree-remove.sh <yol>`: kirli/untracked/ignored dosya varsa reddeder, commit'ler (branch) korunur. Yeni dosyaları `git add` ile stage etmek onları güvenceye almaz; silmeden önce commit et. Testler: `make test-hooks` (CI `hook-tests`).
- `test/*` branch'leri implementasyona (`cmd/`, `internal/`, `migrations/`, `web/src/`), `feat|fix|refactor/*` branch'leri test dosyalarına dokunamaz (CI `pr-policy` job'ı). **Tek istisna:** `testdata/seed/manifest.json` — seed kodu üretir, kodu yazan commit'ler, test yazarı yalnızca okur (`docs/contracts/seed.md`).
- Yeni migration'ı olan faz `make db-roundtrip` hedefini eklemek zorunda; pre-push bunu çağırır.
- Her Docker build context'i (ör. `web/`) kökteki `.dockerignore`'un sır kurallarını kendi `.dockerignore`'unda tekrarlamalı; Docker yalnızca context kökündeki dosyayı okur.

## Test yazarı

Testleri yalnızca `test-writer` ajanı yazar (`.claude/agents/test-writer.md`, kuralları `.claude/skills/writing-tests/SKILL.md`). **Tek kaynak `docs/requirements.md`, istisnasız** (REQ kabul ölçütleri). `docs/contracts/**`, `DECISIONS.md` ve case dokümanı ne test kaynağı ne yardımcı kaynaktır (guard okumayı engeller); implementasyon asla.
- Çalıştırma: `scripts/test-writer-worktree.sh origin/slice/p<N>-<x> test/p<N>-<y>` (implementasyon dizinleri olmadan sparse worktree açar), sonra o dizinde `claude --agent test-writer`.
- `scripts/claude/test-writer-guard.sh` ajana özel PreToolUse hook'u: `cmd/`, `internal/`, `migrations/`, `web/src/`, `docs/contracts/**`, `DECISIONS.md` ve case HTML okunamaz; yalnızca test dosyaları yazılabilir; Bash yalnızca güvenli git/ls komutları ve hedefi test yolu, `docs/requirements.md` veya `docs/PLAN.md` olan `grep`/`rg`.
- Gereken somut ad/yol/alan `docs/requirements.md`'de yoksa test yazılmaz: test yazarı REQ cümlesi önerisiyle "REQ eksik" diye sahibe döner, sahip/mimar ekler, sonra test yazılır. Yalnızca sözleşmede/`DECISIONS.md`'de olan davranışlar için test yazılamaz. Review bulgusu test gerektiriyorsa önce REQ ölçütü eklenir; çözülebilir `// REQ-…` yorumu olmayan test review'da reddedilir (skill §1, `docs/PLAN.md` §3.1 madde 7).
- Bir kural tek dosyada yaşar: yazmadan önce REQ ID'si test yollarında aranır, varsa mevcut test genişletilir; review bulguları yeni satır/alt test olur, `round2`/`fix`/`review` adlı dosya açılmaz (skill §3.1, `docs/PLAN.md` §4.5, `docs/requirements.md` §İzlenebilirlik `Test katmanı`).

## Çoklu ajan orkestrasyonu

Bir işte birden fazla subagent kullanılacaksa orkestratör **Opus 5.5** (`claude-opus-5-5`) modelidir: işi böler, rolleri (mimar, test yazarı, kodu yazan, reviewer, doğrulayıcı) atar ve sonuçları birleştirir. Rol ayrımı kuralları `docs/PLAN.md` §3'te; test yazarı ayrı session'da çalışır ve implementasyona bakmaz.

## Uygulama planı

Fazlar, git akışı, hook'lar, roller (test yazarı implementasyona bakmaz), test kuralları, seed ve güvenlik önlemleri: `/Users/gulcintas/Desktop/SmartCampaignComposer/docs/PLAN.md`. Plandaki VARSAYIM'lar `DECISIONS.md`'ye gerekçesiyle taşınır.

## Zorunlu kurallar (case kısıtları)

Kitle
- Claude'un ürettiği segment kuralı backend'de şemaya göre doğrulanır (izinli alanlar, operatörler, değer tipleri). **LLM çıktısı asla doğrudan SQL olarak çalıştırılmaz**; kural, parametreli sorguya backend kodu tarafından çevrilir.
- Kanal izni olmayan kullanıcıya o kanaldan mesaj gönderilmez (SMS ve email izinleri ayrı).

İçerik
- SMS ≤ 160 karakter, email konusu ≤ 80 karakter. Limit, `{{first_name}}` gibi değişkenler doldurulduktan sonraki metne göre de düşünülmeli.
- `{{first_name}}` vb. kişiselleştirme değişkenleri desteklenir; bilinmeyen değişkenler reddedilir.

Gönderim
- Asenkron kuyruk; **kanal başına saniyede en fazla 100 mesaj**.
- **Aynı kişiye aynı mesaj iki kez gitmez** (idempotency anahtarı: kampanya + kanal + alıcı). Retry stratejisi (backoff, 429 / `Retry-After`, maksimum deneme, dead-letter) bizim tasarımımız ve `DECISIONS.md`'de gerekçelendirilir.

Takip
- Canlı gönderim durumu, metrics ve health check endpoint'leri.

## AI gereksinimleri (değerlendirmenin %25'i — en önemli kısım)

- Claude çıktısı **yapılandırılmış JSON** olmalı (tool use / JSON şema) ve backend'de doğrulanmalı; geçersiz çıktıda hata mesajıyla yeniden deneme / kullanıcıya anlamlı hata.
- LLM erişimi bir arayüz arkasında olmalı ki **testler gerçek API key olmadan, mock LLM ile çalışsın**. Geliştirme de mock LLM ile yapılabilir.

## Hazırlanacak destek bileşenleri

- **Seed:** birkaç bin sahte kullanıcı (ad, email, telefon, şehir, dil, alışveriş/sepet geçmişi, kanal izinleri vb.). Segment kuralı şeması bu alanlarla uyumlu olmalı.
- **Mock SMS/email provider:** ayrı bir servis; retry ve rate limit mantığını gerçekten test edebilmek için rastgele hata, gecikme ve **429** döndürmeli.

## Teslim sözleşmesi

- `docker compose up` — tek komutla tüm sistem (seed verisi ve mock provider dahil).
- `make test` — tüm testler, gerçek API key olmadan.
- `README.md` — kurulum + kısa mimari özeti + yetiştirilemeyenler.
- `DECISIONS.md` — önemli kararlar ve nedenleri.
- `ai-sessions/` — Claude session export'ları (Claude Code'da `/export`). Eklemeden önce API key ve kişisel bilgiler temizlenir.

Bonus (yalnızca çekirdek akış sağlamsa): zamanlanmış gönderim, A/B varyantları, kullanıcının diline göre içerik, CI pipeline.

## Değerlendirme ağırlıkları

LLM entegrasyonu %25 · Mimari ve kararlar %20 · Backend güvenilirliği %20 · Frontend %20 · Test ve kod kalitesi %15. Teslim süresi 7 gün; sonrasında çözüm ve kararlar 30 dakikalık review'da konuşulur — üretilen her kodun anlaşılır ve savunulabilir olması gerekir.

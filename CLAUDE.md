# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Kaynak doküman

Tüm gereksinimlerin tek doğruluk kaynağı: `/Users/gulcintas/Desktop/SmartCampaignComposer/Smart Campaign Composer Case.html` (Insider One · AI Developer Case Study · 2026). Bu dosyayla çelişen bir karar verilecekse önce oraya bak; bilinçli sapmaları `DECISIONS.md`'ye yaz.

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
- **Repo:** GitLab Free, private, `smart-campaign-composer`. Commit'ler İngilizce, Conventional Commits. Case HTML `.gitignore`'da.
- **LLM sağlayıcı seçimi:** `LLM_PROVIDER=auto|real|mock` — key yoksa mock; `docker compose up` ve `make test` key'siz çalışır.

## Komutlar

Araçlar (Homebrew): `go`, `lefthook`, `gitleaks`, `golangci-lint`, `sqlfluff`, `glab`; `go install` ile `goimports`, `govulncheck`.

- `make hooks` — git hook'larını (lefthook) ve commit şablonunu kurar. Klonladıktan sonra ilk iş.
- `make test` — tüm testler konteynerde, key'siz (`compose.test.yml` P2'de gelir; o zamana kadar bilerek hata verir).
- `make test-unit` — host'ta Go unit testleri, DB yok, mock LLM. Tek test: `go test ./internal/<pkg> -run 'TestX_Y/case' -count=1`.
- `make lint`, `make secrets` (tüm geçmişte gitleaks), `make sanitize-sessions`, `make check-sessions`.

Git kuralları (ayrıntı `docs/PLAN.md` §1–2):
- Branch: `<tür>/p<faz>-<kebab>` (`slice|test|feat|fix|refactor|docs|chore|ci|sec`). Commit: Conventional Commits, `feat`/`fix`/`test` için `Refs: REQ-XXX-NN` zorunlu.
- `main`'e doğrudan push yok; MR'ları sahip GitLab arayüzünden merge eder. `scripts/claude/guard-git.sh` (PreToolUse hook) ajanlar için `main`'e push, `--force`, `--no-verify` ve `glab mr merge`'ü engeller.
- `test/*` branch'leri implementasyona (`cmd/`, `internal/`, `migrations/`, `web/src/`), `feat|fix|refactor/*` branch'leri test dosyalarına dokunamaz (CI `role-paths` job'ı).
- Yeni migration'ı olan faz `make db-roundtrip` hedefini eklemek zorunda; pre-push bunu çağırır.
- Her Docker build context'i (ör. `web/`) kökteki `.dockerignore`'un sır kurallarını kendi `.dockerignore`'unda tekrarlamalı; Docker yalnızca context kökündeki dosyayı okur.

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

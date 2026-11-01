# UI sözleşmesi (Vue 3 + TypeScript)

Kapsam: REQ-AUD-03, REQ-AUD-05 (UI), REQ-CNT-01..05 (UI kısımları), REQ-APR-01..03, REQ-TRK-01, REQ-AI-03, REQ-UI-01, REQ-SCH-04 (P7a), REQ-LNG-04 (P8), REQ-ABT-04 (P9), REQ-DSH-02, REQ-DSH-03 (P10).
Görsel dil ve ekran düzeni: kök dizindeki `DESIGN.md` (bağlayıcı değil; çelişkide bu dosya kazanır). API: [`openapi.yaml`](openapi.yaml), akışlar: [`sse-events.md`](sse-events.md).

## 0. Test kuralları ve modül yolları

- Araçlar: Vitest + Vue Test Utils + jsdom. Seçiciler **yalnızca** `[data-testid="…"]`. Durum iddiaları yalnızca bu dosyada adı geçen öznitelikler (`data-state`, `data-status`, `data-phase`, `data-kind`, `data-code`, `data-value`, `data-count`, `data-variant`, `data-provider`, `data-reason`, `data-campaign-id` (P10), `data-loading`, `aria-*`, `role`, `href` (P10, satır linkleri), `sandbox`, `srcdoc`, `title`) ve `textContent` üzerinden yapılır; CSS sınıfı ve görünür Türkçe metin seçici olarak kullanılmaz.
- Metin iddiaları gerektiğinde beklenen değer `t(id, params)` ile üretilir (Türkçe metin testte sabit yazılmaz).
- Sayılar ekranda `Intl.NumberFormat('tr-TR')` ile biçimlenir (`1.482`); ham değer her zaman `data-value` özniteliğindedir — testler `data-value`'yu iddia eder.
- Modül yolları (`@` = `web/src`), testlerin import edebileceği tek yollar:

| Yol | İçerik |
|---|---|
| `@/components/<Ad>.vue` | §3'teki her domain bileşeni (dosya adı = bileşen adı) |
| `@/pages/<Ad>.vue` | §1'deki sayfalar |
| `@/composables/useContentGeneration.ts`, `useCampaignStatusStream.ts`, `useActivityFeed.ts` | §5 |
| `@/api/sse.ts` | `createSSEParser` (§5.1) |
| `@/api/client.ts` | `createApiClient`, `ApiClientKey` |
| `@/api/errors.ts` | `ApiError`, `errorPresentation` (§4) |
| `@/lib/schedule.ts` | Zamanlanmış gönderim saat yardımcıları (§3.26; TSİ = Europe/Istanbul) |
| `@/api/types.ts` | OpenAPI şemalarının TS karşılıkları (aynı adlar: `Campaign`, `ContentVersion`, `Preview`, `CampaignStatus`, `Delivery`, `FieldError`, `WorstCase`, `Audience`, P10: `CampaignSummary`, `CampaignSummaryPage`, `CampaignOverview`, `CampaignStateCounts`, …); içerik akışı olay tipleri: `ContentDeltaEvent`, `ContentInvalidEvent`, `ContentValidatedEvent` (openapi bileşenleriyle aynı ad) |
| `@/i18n/index.ts` | `t(id: MessageId, params?: Record<string, string \| number>): string`, `messages: Record<MessageId, string>` |

Bağımlılık enjeksiyonu (testlerde sahte verilir):

```ts
// @/api/client.ts
export interface ApiClient { request<T>(method: string, path: string, opts?: { body?: unknown; query?: Record<string, string | number>; headers?: Record<string, string> }): Promise<T> } // non-2xx => throws ApiError
export function createApiClient(opts: { baseUrl?: string; fetch?: typeof fetch }): ApiClient
export const ApiClientKey: InjectionKey<ApiClient>
export const FetchKey: InjectionKey<typeof fetch>                 // useContentGeneration
export const EventSourceFactoryKey: InjectionKey<(url: string) => EventSourceLike>
export interface EventSourceLike {
  readonly readyState: 0 | 1 | 2
  addEventListener(type: string, listener: (ev: { data: string; lastEventId?: string }) => void): void
  onerror: ((ev: unknown) => void) | null
  onopen: ((ev: unknown) => void) | null
  close(): void
}
```

Ortak TS tipleri:

```ts
type ChannelKey = 'sms' | 'email'
type FieldKey = 'sms-body' | 'email-subject' | 'email-body'      // testid soneki; API path: sms.body | email.subject | email.body
type ContentPhase = 'idle' | 'streaming' | 'retrying' | 'validated' | 'invalid' | 'interrupted'
type VersionStatus = 'none' | 'streaming' | 'validated' | 'invalid' | 'interrupted'
type StepId = 'brief' | 'audience' | 'content' | 'approval' | 'tracking'
type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error'
```

`FieldKey` ↔ API `path` eşlemesi: `sms.body` → `sms-body`, `email.subject` → `email-subject`, `email.body` → `email-body`, `brief` → `brief`.

## 1. Rotalar, sayfalar ve 5 adımlı akış

| Rota | Ad | Sayfa | Sayfa kök testid |
|---|---|---|---|
| `/` | `brief-new` | `BriefPage` (kampanya yok) | `page-brief` |
| `/campaigns/:campaignId` | `campaign` | yönlendirme (aşağıdaki tablo) | — |
| `/campaigns/:campaignId/brief` | `brief` | `BriefPage` (salt okunur brief) | `page-brief` |
| `/campaigns/:campaignId/audience` | `audience` | `AudiencePage` | `page-audience` |
| `/campaigns/:campaignId/content` | `content` | `ContentPage` | `page-content` |
| `/campaigns/:campaignId/approval` | `approval` | `ApprovalPage` | `page-approval` |
| `/campaigns/:campaignId/tracking` | `tracking` | `TrackingPage` (dashboard) | `page-tracking` |
| `/:pathMatch(.*)*` | `not-found` | `NotFoundPage` | `page-not-found` |

Her sayfanın `h1`'i `page-title` testid'ini taşır, `tabindex="-1"`'dir ve adım değişince odak ona taşınır. Sayfada tek `h1` vardır; `ContentPage` ve `ApprovalPage`'de bu `h1`'i `StepHeader` (§3.2b) çizer.

Kampanya durumu → yönlendirme ve adım erişimi (`CampaignStepper`):

| Kampanya durumu | `/campaigns/:id` yönlendirir | Erişilebilir (tıklanabilir) adımlar | Kilitli adımlar |
|---|---|---|---|
| `draft` | `brief` | brief | audience, content, approval, tracking |
| `audience_ready` | `audience` | brief, audience, content | approval, tracking |
| `content_ready` | `content` | brief, audience, content, approval | tracking |
| `approved` | `approval` | brief, audience, content, approval | tracking |
| `scheduled` (P7a) | `tracking` | tüm adımlar (1–3 salt okunur: editör `locked`; onay adımı §3.17 `scheduled` görünümü) | — |
| `sending`, `completed` | `tracking` | tüm adımlar (1–4 salt okunur: editör `locked`) | — |

P10 (D65): kampanya listesi için **yeni rota yoktur**; genel bakış `brief-new` rotasındaki `BriefPage`'in altındaki `CampaignPanel`'dir (§3.31). `AppHeader`'daki logo linki her ekrandan buraya döner; başlığa ayrıca link eklenmez, stepper bu rotada çizilmez.

Kilitli bir adımın rotasına doğrudan gidilirse yönlendirme tablosundaki rotaya yönlendirilir. Bilinmeyen kampanya (404 `campaign_not_found`) → aynı rotada `EmptyState kind="campaign_not_found"`.

Akış: Brief (`POST /api/campaigns` → `POST /segment`) → Kitle (`POST /content`, SSE) → İçerik (düzenleme `POST /content-versions`) → Onay (`POST /approve` → `POST /send`) → Takip (`GET /events` SSE + `GET /deliveries`).

Not: DESIGN.md §8'deki "görünür olduğu durumlar" tablosu ile bu tablo çelişirse **bu tablo geçerlidir** (ör. İçerik adımı `audience_ready`'de erişilebilirdir, çünkü içerik orada üretilir).

### 1.1 Sayfa eylemleri ve tetikleme kuralları

| Sayfa | Testid | Ne zaman etkin (değilse `aria-disabled="true"`, tıklama etkisiz) | Tıklayınca |
|---|---|---|---|
| `BriefPage` | `brief-submit` (§3.5) | §3.5 kuralları; yalnız `brief-new` rotasında görünür | `POST /api/campaigns` → başarılıysa `POST /api/campaigns/{id}/segment` (beklerken `AudienceLoadingPreview`, §3.7b) → başarılıysa `audience` rotasına geçer. Segment 502/409 olursa sayfa `brief` rotasına geçer ve `ErrorCard` (`action="retry"`) gösterir; `error-card-action` yalnız segment isteğini tekrarlar |
| `AudiencePage` | `audience-generate-content` | kampanya `audience_ready`, `content_ready` veya `approved` **ve** `audience.matched > 0` | `latest_content_version` varsa önce `ConfirmDialog kind="regenerate"`; onaylanınca (veya sürüm yoksa doğrudan) `content` rotasına `?generate=1` sorgusuyla geçer; P9: `audience-ab-toggle` açıksa sorgu `?generate=1&ab=1` olur (§3.29) |
| `AudiencePage` | `audience-edit-brief` | her zaman (kampanya `sending`/`completed` değilse) | API brief güncellemeyi desteklemediği için **yeni kampanya** başlatır: `brief-new` rotasına eski brief metni `BriefComposer.modelValue`'ya önceden doldurulmuş olarak geçer |
| `ContentPage` | `content-regenerate` | üretim sürmüyorken (`versionStatus !== 'streaming'`) ve kampanya `audience_ready`, `content_ready` veya `approved` | `validated` bir sürüm varsa önce `ConfirmDialog kind="regenerate"`; sonra `useContentGeneration().start(campaignId, { ab })` (`ab` = `content-ab-toggle` değeri, P9). Son `invalid` veya `interrupted` sonrası birincil eylemdir |
| `ContentPage` | `content-to-approval` | `versionStatus === 'validated'`, editör `dirty` değil ve kampanya `content_ready` veya `approved` | `approval` rotasına geçer |
| `ApprovalPage` | `approval-approve` / `approval-send` (§3.17) | §3.17 kuralları | `ConfirmDialog` (`approve` / `send`) → onayda `POST /approve` / `POST /send`; `send` başarılıysa `tracking` rotasına geçer. `send` diyaloğu plan kipini taşır (§3.18 `allowSchedule`): "İleri tarihte gönder" seçilirse `POST /send` gövdesine `scheduled_at` eklenir (§3.24) |
| `ApprovalPage` | `approval-schedule-cancel` (§3.17 `scheduled`) | kampanya `scheduled` | `DELETE /api/campaigns/{id}/schedule` (onay diyaloğu yok; plan geri alınabilir bir karardır) |
| `BriefPage` (`CampaignPanel`, P10) | `brief-scheduled-more` / `brief-sent-more`, `brief-scheduled-retry` / `brief-sent-retry`, `campaign-summary-item` (§3.31) | "daha fazla": bölümde gösterilen satır < `total`; yeniden deneme: bölümün son isteği hata verdi; satır linki her zaman | daha fazla: aynı `state`/`order`, `limit: 5`, `offset` = gösterilen satır sayısı, satırlar sona eklenir; yeniden deneme: başarısız isteği aynı sorguyla yineler; satır: `tracking` rotası |
| `TrackingPage` | `schedule-cancel`, `schedule-send-now` (§3.25) | kampanya `scheduled` | iptal: `DELETE …/schedule` → `approval` rotası; şimdi gönder: `ConfirmDialog kind="send"` (`allowSchedule=false`) → `POST /send` (gövdede `scheduled_at` yok) |

`useContentGeneration().start()` **yalnızca** şu iki durumda çağrılır: (a) `ContentPage` mount edildiğinde rota sorgusu `generate=1` ise — bir kez (P9: `ab=1` de varsa `content-ab-toggle` açık başlar ve `start(id, { ab: true })`), ardından `generate` ve `ab` sorguları `router.replace` ile kaldırılır; (b) `content-regenerate` (gerekiyorsa diyalog onayından sonra). `ContentPage` mount'ta başka hiçbir durumda üretim başlatmaz; `latest_content_version` varsa onu `validated` olarak gösterir (alan metinleri sürümden, `worstCase` sürümün `worst_case`'inden).

Önizleme isteği (`GET /api/campaigns/{id}/preview`) şu anlarda gönderilir: `ContentPage`'de `versionStatus` `validated`'a geçtiğinde ve mount'ta `latest_content_version` varsa; düzenleme kaydı (`POST /content-versions`) başarılı olduğunda; `ApprovalPage` mount'unda; `sms-preview-next` tıklandığında (`sample_index = (i+1) mod min(sample_count, 5)`; arayüz yalnız ilk 5 örneği gezer, D49). Akış sürerken, `invalid`/`interrupted` iken ve editör `dirty` iken önizleme istenmez (son başarılı önizleme gösterilmeye devam eder).

## 2. Kabuk

`App.vue`: `AppHeader` + `CampaignStepper` (kampanya rotalarında) + `ConnectionBanner` + `RouterView` + `Toaster`. `GET /api/config` uygulama açılışında bir kez çağrılır; sonucu `ProviderBadge`'e gider.

## 3. Bileşenler

Biçim: **Props** (ad: tip, varsayılan) · **Emits** · **Testid'ler**. Bütün bileşenler LLM/kullanıcı metnini yalnızca metin interpolasyonu (`{{ }}`/`textContent`) ile gösterir (§6).

### 3.1 `Button` (shadcn tabanlı)
Props: `variant: 'primary'|'secondary'|'ghost'|'danger-ghost'` (`primary`), `size: 'sm'|'md'|'lg'` (`md`), `loading: boolean` (false), `disabled: boolean` (false). Emits: `click` — `disabled` veya `loading` iken **emit etmez**. Kök `<button>`: `disabled` iken `aria-disabled="true"` (odaklanabilir kalır, `disabled` HTML özniteliği konmaz), `loading` iken `data-loading="true"`. Kendi testid'i yoktur; ebeveynin verdiği `data-testid` köke düşer.

### 3.2 `AppHeader`
Props: `provider: 'mock'|'claude'|null`, `providerLoading: boolean` (false), `campaignState: CampaignState|null`. Testid'ler: `app-header`; içinde `ProviderBadge` ve kampanya durumu için `StatusBadge` (`data-testid="app-header-campaign-state"`, `kind="campaign"`; `campaignState` null ise yok).

### 3.2a `BrandMark` (marka logosu)
Tek izinli marka görseli `/brand/insider-one-logo.jpeg` (DESIGN.md §1 istisnası). Props: `size` (24), `muted` (false; `opacity-40 grayscale`), `decorative` (true; `alt=""`, aksi halde `t('app.logoAlt')`). `<img data-testid="brand-logo">`, `width`/`height` her zaman verilir. Yerleşim: `EmptyState` (ikonun üstünde, muted), `BriefPage` ilk yükleme skeleton'ı (balonun solunda, muted), `EmailPreview` çerçeve şeridi (`email-preview-brand`, `aria-hidden`; iframe'in **dışında**, `srcdoc` değişmez). Favicon: `index.html` `/brand/favicon-{16,32}.png` (aynı dosyadan türetilmiş) + SVG yedek.

### 3.2b `StepHeader` (adım başlığı)
Props: `step: StepId`, `role: string` (adımın rolü, ör. "Üret ve düzenle"), `title: string`, `subtitle: string` (tek satır amaç), `statusKind: 'content'|'campaign'`, `status: string|null`, `next: string|null`; varsayılan slot (başlığın altındaki ek satır). Testid'ler: `step-header` (`data-step`), içinde `page-title` (`h1`), `status` ≠ null iken `step-header-status` (`StatusBadge`, `data-kind`/`data-status`), `next` ≠ null iken `step-header-next` (metin `t('stepHeader.next')` + `next`). Üst satır (overline) `t('stepHeader.step', { n, total })` · `role`; adım numarası <768 px'te gizlenir (kompakt stepper zaten gösterir).

### 3.2c `LimitChip` (kaldırıldı, P6)
Onay ekranı yeniden tasarımında (P6) tek geçti/kaldı sinyali `approval-limit-check` (§3.24) ile değiştirildi; bileşen ve `limit.state.*` metinleri silindi.

### 3.3 `ProviderBadge` (REQ-AI-03)
Props: `provider: 'mock'|'claude'|null`, `loading: boolean` (false). Testid: `provider-badge` — `data-provider="mock"|"claude"|"unknown"` (null → `unknown`), metin `t('provider.mock')` / `t('provider.claude')` / `t('provider.unknown')`; `loading` iken `data-state="loading"`, aksi `data-state="ready"`. `GET /api/config` → `llm_provider: "mock"` ise rozet `data-provider="mock"`.

### 3.4 `CampaignStepper`
Props: `campaignState: CampaignState|null`, `current: StepId`. Emits: `navigate(step: StepId)` — yalnız erişilebilir adım için. Testid'ler: `stepper`; adım başına `stepper-step-<step>` (`brief|audience|content|approval|tracking`) — `data-status="completed"|"active"|"upcoming"|"locked"`, kilitliyse `aria-disabled="true"`. Kurallar §1 tablosu; `current` olan `active`, ondan önceki erişilebilirler `completed`, sonraki erişilebilirler `upcoming`.

### 3.5 `BriefComposer` (REQ-AUD-05)
Props: `modelValue: string`, `submitting: boolean` (false), `readonly: boolean` (false), `fieldErrors: FieldError[]` ([]). Emits: `update:modelValue(v: string)`, `submit(brief: string)`, `use-example()`.
Testid'ler: `brief-input` (textarea; `readonly`/`submitting` iken `readonly` özniteliği), `brief-counter` (metin `n/2000`, `data-count`=n; `data-state`: n < 1800 `normal`, 1800 ≤ n ≤ 2000 `near-limit`, n > 2000 `over-limit`; n = `[...modelValue.normalize('NFC').trim()].length` — API ile aynı: NFC + trim **sonrası** kod noktası), `brief-submit` (Button; trim sonrası boş, n > 2000 veya `submitting` iken `aria-disabled="true"` ve `submit` emit edilmez), `brief-example`, `inline-error-brief` (`fieldErrors` içinde `path: "brief"` varsa). Ctrl/⌘+Enter `submit` emit eder (aynı koşullarla); Enter yeni satırdır.

### 3.6 `ChatBubble`
Props: `role: 'user'|'assistant'`, `text: string`, `pending: boolean` (false). Testid'ler: `chat-bubble` (`data-role`), `chat-bubble-text` (textContent = `text`), `pending` iken `chat-bubble-pending`.

### 3.7 `AudienceSummaryCard` (REQ-AUD-03, REQ-AUD-04)
Props: `audience: Audience|null`, `loading: boolean`, `error: ApiError|null`, `provider: LlmProvider|null` (null; segmenti o an üreten LLM — `AudiencePage` yalnız `segmentGenerating` iken `config.provider`'ı geçer, salt yüklemede null). Emits: `edit-brief()`, `retry()`.
Testid'ler: `audience-card` (`data-state`: `loading` | `ready` | `empty` (matched = 0) | `error`); `ready`/`empty` iken: `audience-count-matched`, `audience-count-sms`, `audience-count-email` (`data-value` = API sayısı, metin tr-TR biçimli), `audience-bar-sms`, `audience-bar-email` (`role="progressbar"`, `aria-valuenow` = kanal sayısı, `aria-valuemax` = matched), `audience-consent-note` (her zaman, metin `t('audience.consentNote')`); `empty` iken `audience-empty`; `error` iken `ErrorCard`.
**P8 (REQ-LNG-04):** `audience.languages` varsa (`ready`/`empty` iken) kartta `audience-languages` bölümü bulunur: `audience-lang-sms-tr`, `audience-lang-sms-en`, `audience-lang-email-tr`, `audience-lang-email-en` (`data-value` = sayı, metin tr-TR biçimli) ve başlık `t('audience.languages')`; `languages` yoksa (P8 öncesi kampanya) `audience-languages` DOM'da bulunmaz. Mevcut `audience-count-*` öğeleri değişmez.
**P9 (REQ-ABT-04):** `AudiencePage`'de `audience-generate-content`'in yanında `AbToggle` (§3.29) `audience-ab-toggle` bulunur (kartın değil sayfanın öğesidir; `audience-generate-content` ile aynı koşullarda görünür ve onunla **birlikte** etkin/devre dışıdır: `audience-generate-content` `aria-disabled` iken, ör. `draft` veya `audience.matched = 0`, anahtar da `aria-disabled="true"` ve değeri değişmez).
`loading` iken (`aria-busy="true"`) gövde `AudienceSkeleton`'dır (§3.7a): `audience-loading-status` (`role="status"`, görünür, tek duyuru; metin `provider === 'claude'` ise `t('audience.loading.claude')`, aksi halde `t('audience.loading')`; eşleşen-sayı etiketinin yerinde durur) + hazır düzeni satır satır taklit eden `aria-hidden` `Skeleton variant="shimmer"` blokları (44 px sayı bloğu, SMS/Email satırları: gerçek nokta + etiket, 8 px çubuk, 28 px sayı satırı). Sabit yükseklikler sayesinde `ready`/`empty`'ye geçişte kart yüksekliği ve alttaki öğeler kaymaz (`empty` uyarı kutusu hariç). Ayrı `sr-only` durum metni yoktur.
`AudiencePage`: segment yokken ve kart hatası yokken kural alanında `SegmentRuleSkeleton` (§3.7a), testid `segment-rule-loading`.

### 3.7a `AudienceSkeleton`, `SegmentRuleSkeleton` (paylaşılan yükleniyor parçaları)
`AudienceSkeleton` — Props: `provider: LlmProvider|null` (null). §3.7'deki yükleniyor gövdesi (`audience-loading-status` + `aria-hidden` shimmer satırları); tek `role="status"` budur. `SegmentRuleSkeleton` — prop yok; gerçek başlık `t('audience.ruleTitle')` + `aria-hidden` shimmer chip satırı (`SegmentRuleView` geometrisi: 30 px chip'ler, 32/44 px JSON düğmesi yeri, aynı sarma). İkisi de hem Kitle hem Brief adımında kullanılır; geçiş aynı şekilleri korur.

### 3.7b `AudienceLoadingPreview` (Brief adımı, REQ-AUD-03)
Props: `provider: LlmProvider|null` (null). Testid `brief-audience-preview` (`<section>`, `aria-busy="true"`, `aria-label` = `t('audience.title')`): Kitle kartıyla aynı kart yüzeyi; içinde `SegmentRuleSkeleton` + ayraç + `AudienceSkeleton` + izin notu metni (testid'siz). `BriefPage` bunu segment üretimi sürerken kullanıcı balonunun hemen altında gösterir: `brief-new`'de gönderimden sonra (`store.creating || store.segmentGenerating`), salt okunur `brief`'te `store.segmentGenerating` iken; `provider` = `config.provider`. Eski bekleyen asistan balonunun (`chat-bubble-pending`) yerini alır; sayfada tek yükleniyor göstergesi budur. `brief-submit` bu sürede eskisi gibi kilitli; hata davranışı (§1.1) değişmedi.

### 3.8 `SegmentRuleView`
Props: `rule: object`. Testid'ler: `segment-rule`; her koşul için bir `segment-rule-chip` (`data-field`, `data-op`; sayı = koşul sayısı); `segment-rule-json-toggle`; açıkken `segment-rule-json` (`<pre>`, textContent = `JSON.stringify(rule, null, 2)`).

### 3.9 `ChannelTabs`
Props: `modelValue: ChannelKey`, `phases: Record<FieldKey, ContentPhase>`. Emits: `update:modelValue(c: ChannelKey)`. Testid'ler: `channel-tab-sms`, `channel-tab-email` (`aria-selected`, `data-status` = kanalın özet fazı: kanalın herhangi bir alanı `invalid` ise `invalid`, aksi halde ilk alanının fazı), sekme `invalid` iken `channel-tab-error-sms` / `channel-tab-error-email`.
Erişilebilirlik ve klavye (Reka Tabs): kök `role="tablist"`, `aria-label` = `t('tabs.channels')`; her sekme `role="tab"`, `aria-controls` = `content-panel-<c>` öğesinin kimliği; seçili sekme `tabindex="0"`, diğeri `tabindex="-1"` (yuvarlanan tabindex). `←`/`→` odağı komşu sekmeye taşır ve **seçer** (otomatik etkinleştirme, uçlarda başa/sona sarar; `update:modelValue` emit eder), `Home`/`End` ilk/son sekmeye gider. Sekme içeriği `content-panel-sms` / `content-panel-email` (`role="tabpanel"`, `aria-labelledby` = ilgili sekme); seçili olmayan panel `hidden` özniteliğini taşır ama DOM'dan kaldırılmaz (akış sırasında gizli kanalın `StreamingText`'leri de dolmaya devam eder).

### 3.10 `StreamingText` (REQ-CNT-04, REQ-UI-01)
Props: `field: FieldKey`, `text: string`, `phase: ContentPhase`, `errors: FieldError[]` ([]), `provider: LlmProvider|null` (null; `ContentPage` `GET /api/config` değerini geçirir). Emits: `regenerate()`.
Testid'ler (`<f>` = `field`): `streaming-text-<f>` (`data-phase` = `phase`, `aria-busy="true"` yalnız `streaming`/`retrying`), `streaming-text-content-<f>` (textContent = `text`), `streaming-text-badge-<f>` (`StatusBadge kind="content"`; `data-status`: `streaming`/`retrying`/`idle` → `draft`, diğerleri aynı adla `validated`/`invalid`/`interrupted`), `streaming-text-caret-<f>` (yalnız `streaming`), `streaming-text-retrying-<f>` (yalnız `retrying`), `streaming-text-waiting-<f>` (bekleme durumu: `phase` `streaming`/`retrying` **ve** `text === ''`; ilk metin gelince kalkar; içinde `t('stream.waiting.claude')` (`provider === 'claude'`) veya `t('stream.waiting')` satırı + 3 `aria-hidden` `Skeleton variant="shimmer"` satırı (12 px), `prefers-reduced-motion`'da animasyonsuz (sabit turuncu ton); boş `<p>` yerine gösterilir, `streaming-text-content-<f>` bu sürede DOM'da yoktur; `aria-live` içermez, duyuru yalnız `content-live-status` faz değişiminde), `streaming-text-interrupted-<f>` + `streaming-text-regenerate-<f>` (yalnız `interrupted`), `streaming-text-errors-<f>` (yalnız `invalid`; içinde her hata için bir `field-error`, `data-code` = hata kodu).

### 3.11 `StatusBadge`
Props: `kind: 'content'|'delivery'|'campaign'`, `status: string` (content: `draft|validated|invalid|interrupted`; delivery: `queued|sending|sent|failed`; campaign: 7 kampanya durumu, `scheduled` dahil: ton `info`, ikon `CalendarClock`, metin `t('status.campaign.scheduled')`), `size: 'sm'|'md'` (`md`). Kök testid'i ebeveynden gelir (varsayılan `status-badge`); `data-kind`, `data-status`; metin `t('status.<kind>.<status>')`; her zaman ikon + metin (renk tek başına değil).

### 3.12 `VariableChip` (REQ-CNT-03)
Props: `name: string`, `variant: 'insert'|'inline'|'invalid'`, `disabled: boolean` (false). Emits: `insert(name: string)` (yalnız `insert` ve `!disabled`). Testid: `variable-chip-<name>` (`data-variant`); metin `{{<name>}}`. `ContentEditor` araç çubuğunda yalnız `first_name`, `last_name`, `city` için `insert` chip'leri bulunur; düzenleme metnindeki izinsiz değişkenler (`{{password}}`) için önizleme altında `invalid` chip gösterilir.
`invalid` chip'leri yalnız sunum amaçlıdır ve istemcide düzenleme metninden türetilir (doğrulama değildir; asıl hata `inline-error-<f>` ile backend'den gelir): `\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}` ile bulunan adlardan `first_name|last_name|city` dışında kalanlar, tekilleştirilerek, önizlemenin altında bir `invalid` chip olarak sıralanır. Bozuk söz dizimi (`{{first_name`) chip üretmez; onu backend `malformed_variable` olarak bildirir.

### 3.13 `CharCounter` (REQ-CNT-01, -02, -05)
Props: `field: 'sms-body'|'email-subject'`, `text: string`, `limit: 160|80`, `worstCase: number|null` (null).
Sayım: `n = [...text.normalize('NFC')].length` (şablon metni, değişkenler yazıldığı gibi). Eşikler (DESIGN V5): `normal` n < 0.9×limit (SMS n < 144, konu n < 72) · `near-limit` 0.9×limit ≤ n ≤ limit (144–160 / 72–80) · `over-limit` n > limit.
Testid'ler (`<f>` = `field`): `char-counter-<f>` (`data-state`, `aria-live="polite"`), `char-counter-value-<f>` (metin `n/limit`, `data-count`=n), `char-counter-over-<f>` (yalnız `over-limit`, metin `−(n−limit)`), `char-counter-worst-<f>` — yalnız `worstCase !== null` iken: ikinci satır, metin `t('counter.worstCase', { n: worstCase })`, `data-count` = worstCase, `data-state` aynı eşiklerle `worstCase` üzerinden.
Düzenlenebilir alanlarda `n` hiçbir zaman `limit`'i yeni girişle aşmaz (§3.14 d2); `over-limit` yalnız limiti zaten aşan metinde (invalid taslak) veya salt okunur gösterimde görünür.
Kaynak: `worstCase` = API `ContentVersion.worst_case.sms_runes` / `.subject_runes` (veya `validated` olayının `worst_case`'i); kaydedilmemiş düzenlemede son kaydedilen sürümün değeri gösterilmez (null).

### 3.14 `ContentEditor` (REQ-APR-01, -02, -03)
Props: `modelValue: Content`, `channel: ChannelKey`, `language: 'tr'|'en'` (`'tr'`; P8), `mode: 'readonly'|'editable'|'locked'`, `dirty: boolean`, `saving: boolean` (false), `fieldErrors: FieldError[]` ([]), `worstCase: WorstCase|null` (null), `conflict: { currentVersionId: string | null; currentVersionNumber: number | null } | null` (null). Emits: `update:modelValue(c: Content)`, `save(c: Content)`, `reload-latest()`, `keep-mine()`.
Ayrıntılar: (a) `modelValue` **her zaman tam** `Content`'tir (iki kanal); `channel` yalnız hangi alanların gösterildiğini belirler ve `save(c)` tam içeriği gönderir (API gövdesi tam içeriktir). (b) `dirty` ebeveynindir: `modelValue`, taban içerikten (son `validated` sürümün içeriği; `invalid` taslak düzenlenirken taslak metinleri) alan alan katı dize eşitliğiyle farklıysa `true`. (c) `editor-save` ve alanlarda **Ctrl/⌘+Enter** aynı koşullarla `save` emit eder (kullanılabilirken); Enter metin alanında yeni satırdır, konu `input`'unda bir şey yapmaz. (d) Değişken chip'i tıklanınca `{{<ad>}}` **son odaklanan alanın** seçimine yazılır (seçim varsa yerini alır), imleç eklenen metnin sonuna konur, odak alana döner, `update:modelValue` emit edilir; hiç odaklanılmadıysa hedef kanalın gövde alanıdır (`sms-body` / `email-body`). (d2) **Giriş sınırı (sahip kararı, D48):** `editor-sms-body` ve `editor-email-subject` alanları, girişin sonucu `CharCounter` sayısını (`n`, §3.13 ile aynı NFC rune sayımı, şablon metni) `limit`'in (SMS 160, konu 80) üstüne çıkaracaksa girişi o kadarıyla **kırpar**: yazma, yapıştırma, sürükle-bırak ve chip ekleme limite kadar uygulanır (yapıştırılan metnin sığan öneki eklenir; sığmayan chip eklenmez, `update:modelValue` emit edilmez). Kırpma rune sayımıyla yapılır; native `maxlength` kullanılmaz (UTF-16 birimi sayar). Böylece düzenlenebilir alanda sayaç en çok `limit/limit` (`near-limit`) gösterir ve yeni girişle `over-limit` oluşamaz. Limiti zaten aşan metin (LLM'in `invalid` taslağı, `content-edit-draft`) olduğu gibi yüklenir ve sayaç gerçek sayıyı ve `over-limit`'i gösterir; `n > limit` iken yalnız sayıyı **artırmayan** girişler (silme, aynı uzunlukta veya kısa değiştirme) uygulanır, artıranlar kırpılır. Salt okunur/akış gösterimlerinde (`readonly`, `locked`, `streaming`) sayaç gerçek sayıyı gösterir. E-posta gövdesine bu kural uygulanmaz (5000 yalnız doğrulamadadır). `worst_case` sınırı ayrıdır: şablon limitin altında olsa da değişkenler dolunca aşabilir; bu durumda API 422 `*_too_long_worst_case` döner ve `char-counter-worst-<f>` `over-limit` gösterir (değişmedi). (e) Her alan: kendi `fieldErrors` girdisi varsa veya sayacı `over-limit` ise `aria-invalid="true"`; `aria-describedby` alanın `char-counter-<f>` (varsa) ve `inline-error-<f>` (varsa) öğelerine işaret eder. `path: ""` alan hataları her alanın `inline-error-<f>`'ine eklenir (§5.2 ile aynı kural). (f) `saving` iken alanlar `readonly`, `editor-save` `aria-disabled`. (g) **Çakışma durumu** (`conflict` ≠ null; sunucu 409 `content_edit_conflict` döndü): `editor-conflict` görünür (`role="alert"`, `data-current-version-id` = `currentVersionId` veya boş, `data-value` = `currentVersionNumber` veya boş; metin `t('editor.conflict.title', { n })`), içinde `editor-conflict-reload` (metin `t('editor.conflict.reload')`, tıklayınca `reload-latest` emit) ve `editor-conflict-keep-mine` (metin `t('editor.conflict.keepMine')`, `keep-mine` emit, `aria-describedby` = `editor-conflict-warning`'in kimliği). Bu ikisinin altında, düğmeye basılmadan **her zaman görünen** açık bir uyarı vardır: `editor-conflict-warning` (metin `t('editor.conflict.keepMineWarning')`; önerilen metin: "Metnini korursan diğer sekmedeki değişiklik geçmişte kalır, yeni sürüm senin metnin olur. Eski sürüm silinmez."). Çakışma sürerken `editor-save` `aria-disabled="true"` ve `save` emit etmez; alanlar düzenlenebilir ve kullanıcı metni **korunur**; `editor-conflict` açılınca odak `editor-conflict-reload`'a gider.
**P8:** `language="en"` iken alanlar `modelValue.en.*` alanlarını düzenler (testid'ler aynı: `editor-sms-body`, `editor-email-subject`, `editor-email-body`; bileşen aynı anda yalnız bir dili gösterir) ve `fieldErrors` yalnız `en.` önekli yolları (`en.sms.body`, …) o dilin alanlarına dağıtır; `language="tr"` iken `en.` önekli hatalar bu bileşende gösterilmez. `modelValue.en` yokken `language="en"` verilmez (sayfa sekmeyi göstermez). `save(c)` her zaman bütün içeriği (`en` dahil) yollar.
**P9 (REQ-ABT-04):** ek prop `variant: 'a'|'b'` (`'a'`). `variant="b"` iken alanlar `modelValue.b.*` (`language="en"` iken `modelValue.b.en.*`) alanlarını düzenler; testid'ler aynıdır. `fieldErrors` dağıtımı (varyant, dil) çiftine göredir: (`a`,`tr`) öneksiz yollar; (`a`,`en`) `en.`; (`b`,`tr`) `b.` (ama `b.en.` değil); (`b`,`en`) `b.en.`. `modelValue.b` varken editörde `editor-angle` (`<input>`, `value` = `modelValue.angles[variant]`, etiket `t('editor.angle')`; düzenlenince `update:modelValue` açıyı değiştirir; giriş `AngleMaxRunes` = 60 rune'da kırpılır, §3.14 d2 ile aynı yöntem) ve açı hatası için `inline-error-angle` (`angles.a` hatası yalnız `variant="a"`, `angles.b` hatası yalnız `variant="b"` iken); `variant="b"` iken yol `b` hatası (`variants_identical`) `inline-error-variant`'ta. `modelValue.b` yokken `variant="b"` verilmez ve `editor-angle` DOM'da yoktur. `save(c)` bütün içeriği (`b`, `angles` dahil) yollar.
Testid'ler: `channel="sms"` iken `editor-sms-body` (textarea) + `CharCounter field="sms-body"`; `channel="email"` iken `editor-email-subject` (input) + `CharCounter field="email-subject"` + `editor-email-body` (textarea); `editor-save` (`mode !== 'editable'`, `!dirty` veya `saving` iken `aria-disabled="true"`); `mode` `readonly`/`locked` iken alanlarda `readonly`; `locked` iken `editor-locked-note`; insert chip'leri `variable-chip-first_name|last_name|city` (imleç konumuna ekler, `locked`/`readonly` iken `disabled`); her alan hatası için `inline-error-<f>` (`data-code`). `mode` kuralı: akış sürerken `readonly`; kampanya `scheduled`/`sending`/`completed` ise `locked`; aksi `editable`.

### 3.15 `SmsPreview` (REQ-APR-01, REQ-CNT-03, REQ-UI-01)
Props: `preview: Preview|null`, `loading: boolean`, `error: ApiError|null`. Emits: `next-sample()`. Testid'ler: `sms-preview` (`data-state`: `loading|ready|error|empty`), `sms-preview-text` (textContent **birebir** `preview.sms.text`), `sms-preview-recipient` (textContent = `preview.customer.first_name` boşsa `t('preview.fallbackRecipient')`, aksi `first_name`), `sms-preview-sample` (metin `t('preview.sample', { i: sample_index + 1, n: min(sample_count, 5) })`), `sms-preview-next` (Button; `sample_count ≤ 1` iken `aria-disabled`).
`data-state` ilk eşleşen kural (hem `SmsPreview` hem `EmailPreview`): `loading` (`loading` true; önceki başarılı `preview` varsa metni yerinde kalır) › `error` (`error` ≠ null) › `ready` (`preview` ≠ null) › `empty`. `empty` ve `error` iken metin/çerçeve öğeleri (`sms-preview-text`, `email-preview-frame`, …) DOM'da bulunmaz. `error` durumunda `error.code` `customer_not_found` ise `sms-preview-next` kullanılabilir kalır ve `sample_index=0` istenir (§4).

**P8 (REQ-LNG-04):** `SmsPreview` ve `EmailPreview` ek olarak `preview.language` / `preview.fallback` / `preview.customer.language` alanlarını kullanır. `sms-preview` ve `email-preview` köklerine `data-language` (= `preview.language`) eklenir. Önizlemenin kendi dil seçicisi yoktur (sahip kararı, 2026-10-30; önceki `preview-language` grubu kaldırıldı): önizleme dili sayfadaki `LanguageTabs` seçimini izler. Sekmeler görünürken istek `GET …/preview?language=<seçili>` olur ve sekme değişince yenilenir (`sample_index` korunur); sekmeler yokken istekte `language` yoktur (örnek alıcının kendi dili). `preview.fallback === true` iken `preview-fallback-note` görünür (`role="note"`, metin `t('preview.fallbackNote')`), aksi halde DOM'da yoktur; `ContentPage` ve `ApprovalPage` için aynı.

**P9 (REQ-ABT-04):** `sms-preview` ve `email-preview` köklerine `data-variant` (= `preview.variant`) eklenir. Önizleme varyantı sayfadaki `VariantTabs` seçimini izler: sekmeler görünürken istek `GET …/preview?variant=<seçili>` olur (dil sekmesi de varsa `&language=<seçili>`), sekme değişince yenilenir (`sample_index` korunur); sekmeler yokken istekte `variant` yoktur (yanıt `variant: "a"`).

### 3.16 `EmailPreview` (REQ-APR-01, REQ-UI-01)
Props: `preview: Preview|null`, `loading: boolean`, `error: ApiError|null`. Testid'ler: `email-preview` (`data-state`), `email-preview-subject` (textContent = `preview.email.subject`), `email-preview-frame` — `<iframe>`: `sandbox=""` (**boş değer**, hiçbir `allow-*` yok), `srcdoc` = `preview.email.html` **birebir** (istemci değiştirmez/temizlemez), `title` = `t('preview.emailFrameTitle')`, `src` özniteliği yok.

### 3.17 `ApprovalBar` (REQ-APR-01, -03, REQ-CNT-04, -05)
Props: `campaignState: CampaignState`, `versionStatus: VersionStatus`, `dirty: boolean`, `submitting: boolean` (false), `stale: boolean` (false), `scheduledAt: string|null` (null; `Campaign.scheduled_at`, P7a). Emits: `approve()`, `send()`, `cancel-schedule()`, `to-tracking()`. Varsayılan slot: ikincil eylemler (ör. `approval-edit`), birincil butonun solunda (<640 px altında).
Yerleşim: bileşenin kendisi kart/gölge çizmez ve konumlanmaz; zemini ve yapışıklığı ebeveyn verir (§3.24 `approval-decision`: özet kartın son bloğu, `sticky bottom-0`, kart içinde akışta). ≥640 px tek satır (not solda, eylemler sağda); <640 px not üstte, eylemler alt alta ve tam genişlik, birincil önce.
Not satırı (durum başına bir tane): `approved` → `CircleCheck` + `t('status.campaign.approved')` · `t('approval.approvedNote')` (düzenlemenin onayı düşüreceğini söyler, REQ-APR-03); `ready`/`submitting` → `t('approval.readyNote')`; `stale` → `approval-stale-note`; `disabled` → `approval-reason`.
`stale` prop'unu `ApprovalPage` yönetir (§3.24): onay veya gönderim isteği 409 `stale_content_version` döndürünce `true` olur; kullanıcı bir sonraki onay denemesini başlatınca veya sayfadan ayrılınca `false` olur. `disabled` iken `approval-approve` `aria-describedby` ile `approval-reason` öğesine bağlıdır.
Durum (`approval-bar`'ın `data-state`'i), ilk eşleşen:
0. `campaignState === 'scheduled'` → `scheduled` (P7a): `approval-approve` ve `approval-send` **yok**; not `approval-scheduled-note` (metin `t('approval.scheduledNote', { time: formatTsi(scheduledAt) })`, `data-value` = `scheduledAt`; `CalendarClock` ikonu); eylemler `approval-schedule-cancel` (Button secondary, metin `t('schedule.cancel')`, `cancel-schedule` emit; `submitting` iken `aria-disabled`) ve `approval-to-tracking` (Button primary, metin `t('approval.toTracking')`, `to-tracking` emit).
1. `campaignState` `approved` ve `!stale` → `approved`: `approval-send` görünür (Button primary), `approval-approve` yok.
2. `stale` → `stale`: `approval-stale-note` görünür; `approval-approve` `aria-disabled` değil (yeniden onay).
3. `submitting` → `submitting`.
4. `versionStatus === 'validated'` ve `!dirty` ve `campaignState === 'content_ready'` → `ready`: `approval-approve` etkin.
5. diğer → `disabled`: `approval-approve` `aria-disabled="true"`, tıklama `approve` **emit etmez**; `approval-reason` görünür, `data-reason` ∈ `no_content` (`none`), `streaming`, `invalid`, `interrupted`, `dirty`, `state` (kampanya durumu uygun değil).
Testid'ler: `approval-bar`, `approval-approve`, `approval-send`, `approval-reason`, `approval-stale-note`, `approval-scheduled-note`, `approval-schedule-cancel`, `approval-to-tracking`. Onay/gönderim her zaman önce `ConfirmDialog`'u açar (sayfa sorumluluğu); plan iptali diyalog açmaz.

### 3.18 `ConfirmDialog`
Props: `open: boolean`, `kind: 'approve'|'send'|'regenerate'`, `counts: { sms: number; email: number } | null` (null), `submitting: boolean` (false), `error: ApiError|null` (null). Emits: `confirm()`, `cancel()`, `update:open(v: boolean)`. Testid'ler: `confirm-dialog` (`data-kind`), `confirm-dialog-confirm`, `confirm-dialog-cancel`, `send` iken `confirm-dialog-count-sms`, `confirm-dialog-count-email` (`data-value`), `error` varsa `confirm-dialog-error` (`data-code`; 422 alan hataları içinde `field-error` öğeleri, `data-code` ör. `sms_too_long_worst_case`).
Ek testid: `confirm-dialog-title` (metin `t('confirm.<kind>.title')`). `confirm-dialog-confirm` etiketi: `kind="send"` iken `Send` ikonu + `t('confirm.send.confirm')` ("Gönder"), diğer türlerde `t('confirm.confirm')` ("Onayla"). Erişilebilirlik ve davranış (Reka AlertDialog): kök `role="alertdialog"`, `aria-modal="true"`, `aria-labelledby` = `confirm-dialog-title`; açılınca odak `confirm-dialog-cancel`'a gider (geri alınamaz eylemlerde güvenli varsayılan); odak diyalog içinde döner (Tab/Shift+Tab tuzağı); `Esc` ve arka plana tıklama `cancel` emit eder ve `update:open(false)`; kapanınca odak diyaloğu açan öğeye döner. `submitting` iken `confirm-dialog-confirm` `aria-disabled="true"` ve `confirm` emit etmez, `confirm-dialog-cancel` etkindir ama `Esc`, arka plan ve iptal **kapatmaz** (`cancel` emit edilmez). `error` null değilken diyalog açık kalır ve `confirm-dialog-error` görünür; sayfa başarıda `open`'ı `false` yapar. `confirm` her tıklamada en çok bir kez emit edilir.

**Plan kipi (P7a, REQ-SCH-04).** Ek props: `allowSchedule: boolean` (false), `scheduleMinLeadSeconds: number` (60; `GET /api/config` `limits.schedule_min_lead_seconds`), `scheduleMaxAheadSeconds: number` (2592000; `limits.schedule_max_ahead_seconds`). Ek emit: `schedule(scheduledAt: string)` — `scheduledAt` UTC ISO, biçim `YYYY-MM-DDTHH:MM:00Z`. `allowSchedule` yalnız `kind="send"` iken etkilidir; `false` iken bileşen bugünkü gibi davranır (plan öğeleri DOM'da yoktur, `schedule` emit edilmez).
- `kind="send"` iken kök `confirm-dialog` `data-mode="now"|"later"` taşır (`allowSchedule=false` → her zaman `now`). Diyalog her açılışta `now` kipiyle ve boş zaman alanıyla başlar.
- Kip seçimi: `confirm-dialog-send-mode` (`role="radiogroup"`, `aria-label` = `t('confirm.sendMode.label')`), içinde `confirm-dialog-send-now` (metin `t('confirm.sendMode.now')`) ve `confirm-dialog-send-later` (metin `t('confirm.sendMode.later')`); her biri `role="radio"`, seçili olan `aria-checked="true"`. Tıklama veya ok tuşları kipi değiştirir.
- `later` kipinde: `confirm-dialog-scheduled-at` — `<input type="datetime-local" step="60">`, değer **TSİ duvar saati** `YYYY-MM-DDTHH:MM` (tarayıcının/`TZ`'nin saat diliminden bağımsız, §3.26), `min` = en erken izinli dakika (`now + scheduleMinLeadSeconds`'ın TSİ karşılığı, yukarı tam dakikaya yuvarlanmış), `max` = `now + scheduleMaxAheadSeconds`'ın TSİ karşılığı (aşağı tam dakikaya); etiket `t('confirm.scheduledAt.label')`, yanında `confirm-dialog-tz` (metin `t('schedule.tz')`), altında ipucu `t('confirm.scheduledAt.hint', { min: Math.ceil(scheduleMinLeadSeconds / 60) })` ve izin notu `confirm-dialog-schedule-note` (metin `t('confirm.schedule.audienceNote')`; kanal sayıları gönderim anında değişebilir, D62). Değer `toUtcIso` ile çözülebiliyorsa `confirm-dialog-scheduled-at` `data-value` = UTC ISO taşır, çözülemiyorsa (boş dahil) özniteliği yoktur.
- İstemci denetimi (güvence API'dedir; aynı sınırlar): "şimdi" = `Date.now()` (testler `vi.useFakeTimers()` + `vi.setSystemTime`), alan her `input`'ta ve onay tıklamasında yeniden denetlenir. Boş → hata metni yok, `confirm-dialog-confirm` `aria-disabled="true"`. UTC an < now + `scheduleMinLeadSeconds` → `confirm-dialog-schedule-error` (`data-code="schedule_too_soon"`, metin `t('field_error.schedule_too_soon')`); > now + `scheduleMaxAheadSeconds` → aynı öğe `data-code="schedule_too_far"` (`t('field_error.schedule_too_far')`). Hata varken alan `aria-invalid="true"` ve `aria-describedby` hataya bağlı, `confirm-dialog-confirm` `aria-disabled="true"` ve hiçbir şey emit etmez. Sınırlar dahildir (tam now + minLead geçerlidir; alan dakika hassasiyetli olduğundan bu iddia sahte `now` tam bir dakikadayken test edilir).
- Etiketler: `later` kipinde `confirm-dialog-title` metni `t('confirm.schedule.title')`, gövde `t('confirm.schedule.body')`, `confirm-dialog-confirm` `CalendarClock` ikonu + `t('confirm.schedule.confirm')` ("Planla"); `now` kipinde bugünkü `t('confirm.send.title')` / `t('confirm.send.confirm')` ("Gönder"). Kanal sayıları (`confirm-dialog-count-*`) iki kipte de görünür.
- Onay: `now` kipinde `confirm()`; `later` kipinde geçerli değerle `schedule(scheduledAt)` (yalnız biri, tıklama başına en çok bir kez). Sunucunun 422'si (`schedule_too_soon`/`schedule_too_far`/`invalid_value`, yol `scheduled_at`) bugünkü gibi `error` prop'u ile `confirm-dialog-error` içinde `field-error` öğeleri olarak görünür (`data-code`), diyalog açık ve `later` kipinde kalır.

### 3.19 `ProgressBar` / `ProgressRow`
Props: `value: number`, `max: number`, `variant: 'single'|'segmented'`, `segments: { sent: number; failed: number; sending: number; queued: number } | null` (null), `label: string` (''). Kök `role="progressbar"`, `aria-valuenow` = `value`, `aria-valuemin="0"`, `aria-valuemax` = `max`; testid ebeveynden düşer.

### 3.20 `DeliveryCounters` (REQ-TRK-01)
Props: `status: CampaignStatus|null`, `connection: ConnectionState`, `lastUpdatedAt: string|null`. Testid'ler: `delivery-counters` (`data-state`: `completed` (status.state = completed) › `scheduled` (status.state = scheduled, P7a: sayaç kartları, `delivery-card-*`, `delivery-count-*` ve `delivery-stale-note` render edilmez; plan `SchedulePanel`'dedir, §3.25) › `stale` (connection ≠ `open`) › `live`); kanal `<c>` ∈ `sms|email` için `delivery-card-<c>`, `delivery-bar-<c>` (`aria-valuenow` = sent+failed, `aria-valuemax` = total), `delivery-count-<c>-<k>` (`<k>` ∈ `total|queued|sending|sent|failed`, `data-value` = `status.by_channel.<c>.<k>`); toplam `delivery-count-total-<k>` (`data-value` = `status.counts.<k>`); `stale` iken `delivery-stale-note` — ilk bağlantı henüz kurulurken (`status` null ve `connection` `idle`/`connecting`) gösterilmez, ilk deneme başarısızsa (`error`) gösterilir; `status` null iken sayaç öğelerinde `data-value` yoktur (ekranda "—"); `completed` iken `delivery-completed-title` ve `failed > 0` ise `delivery-failed-link` (`?status=failed` filtresiyle teslimat listesine). Kampanya düzeyinde "başarısız" durumu gösterilmez.
**P9 (REQ-ABT-04):** `TrackingPage`, `status.by_variant` dolu iken (`null` değil ve anahtar var) `delivery-counters`'ın altında `VariantBreakdown` (§3.30) gösterir; `scheduled` görünümünde göstermez. `DeliveryCounters`'ın kendisi değişmez.
**P10 (REQ-DSH-03, D65; yeniden düzen, davranış aynı).** Props, `data-state` kuralı ve yukarıdaki **bütün** testid'ler, `data-value`'lar ve görünürlük kuralları aynen kalır (REQ-TRK-01/SCH-04/ABT-04 testleri değişmez); öğe yeri ve tipografi değişir (DESIGN.md §8.5). Yukarıdan aşağı:
- **Genel durum kartı** (tek vurgulu kart: `--radius-card-lg` + gradient şerit): ilk başlık `h2` (`text-h2`, `--text-accent-heading`), metin `t('tracking.overallTitle')`. Başlık satırının sağında canlılık: `data-state="live"` iken `delivery-live` (pill, metin `t('delivery.live')`), `completed` iken `delivery-completed-title` (metin `t('delivery.completedTitle')`), `stale` iken `delivery-stale-note` (§3.20 kuralı). Durum cümlesi ("Kampanya gönderiliyor") ve bekleme metni artık başlık değildir.
- Büyük sayı `delivery-percent` (`numeral-xl`, `--text-accent-numeral`; `data-value` = ⌊(sent + failed) / total × 100⌋ `status.counts` üzerinden; total 0 iken `completed`'da 100, aksi 0; metin `%` + değer). Yanında `delivery-processed` (`data-value` = sent + failed, metin `t('tracking.processed', { done, total })`); cümledeki toplam sayı `delivery-count-total-total` öğesidir. Altında `delivery-bar-total` (§3.20a, segmentli `ProgressBar`).
- `delivery-results` (başlığı görsel `caption` `t('tracking.results')`): `delivery-count-total-sent`, `delivery-count-total-failed` (`numeral-md`; failed > 0 ise `--error` + etiket). `delivery-remaining` (`t('tracking.remaining')`): `delivery-count-total-sending`, `delivery-count-total-queued` (`numeral-sm`, `--ink-700`). İki grup da `<dl>`.
- Kartın altında `delivery-last-updated` (`caption`): `lastUpdatedAt` doluyken her `data-state`'te (`live`/`stale`/`completed`) görünür, `data-value` = `lastUpdatedAt` aynen (`TrackingPage`'de `useCampaignStatusStream`'in `lastUpdatedAt`'i = son akış olayının `CampaignStatus.updated_at` ISO metni), metin `t('delivery.lastUpdated', { time })`, `time` = `toLocaleTimeString('tr-TR', { hour, minute, second: '2-digit' })` (tarayıcı saat dilimi; testlerde metin iddia edilmez).
- `status` null iken: sayaç öğelerinde `data-value` yok ("—"), `delivery-percent`/`delivery-processed`'de de yok; `Skeleton` blokları + `delivery-waiting` (görsel olarak gizli, `role="status"`, metin `t('delivery.waiting')`); `delivery-counters` içinde bu metni taşıyan başlık yoktur.
- **Kanal kartları** `delivery-card-<c>`: başlık `h3` (`text-h3`, `--text-accent-heading`, kanal noktası + `t('tabs.sms')` / `t('tabs.email')`); kanal toplamı (`delivery-count-<c>-total`) ve `delivery-percent-<c>` (`data-value` = kanalın ⌊(sent + failed) / total × 100⌋, total 0 iken `completed`'da 100, aksi 0) aynı `numeral-md` boyunda çubuğun (`delivery-bar-<c>`) üstünde; dört durum (`delivery-count-<c>-<k>`) tek sütunlu `<dl>` (etiket `body-sm --ink-600`, sayı `numeral-sm` sağa hizalı, `tabular-nums`). ≥ 768 yan yana, < 768 alt alta.
- `delivery-failed-link` aynı kural (§3.20), genel kartın sonunda.
- Başlık düzeni: `delivery-counters` içinde önce bir `h2`, sonra kanal başına bir `h3`; başka başlık düzeyi yok.
- **Planlı görünüm** (`data-state="scheduled"`): `delivery-counters` içinde başlık ("Genel durum" `h2` dahil) ve `delivery-percent`, `delivery-processed` (içindeki `delivery-count-total-total` ile), `delivery-results`, `delivery-remaining`, `delivery-count-*`, `delivery-card-*`, `delivery-percent-<c>`, `delivery-live`, `delivery-last-updated`, `delivery-waiting`, `delivery-stale-note` render edilmez (REQ-SCH-04: sıfır `delivery-count-*`); plan `SchedulePanel`'dedir.
- **Bayatlıkta opaklık düşürülmez** (`opacity-*` sınıfı kaldırılır; kontrast korunur). Hareket kuralları (§5 DESIGN sayaç tween'i, reduced-motion) değişmez. Tek kart dili: `surface-card`, `--radius-card`, `--shadow-card` (kanal kartları, varyant tablosu, başarısız listesi); yalnız genel durum kartı `--radius-card-lg` + şerit.

### 3.20b `TrackingPage` başlık bloğu (REQ-DSH-03, P10)
`StepHeader` (§3.2b; `h1` `page-title` `t('tracking.title')`, tek durum rozeti `step-header-status`) korunur; genel alt başlık (`tracking.subtitle`) yerine varsayılan slot'a kampanyanın kimliği konur (kampanya yüklendikten sonra):
- `tracking-campaign-brief` — `textContent` = `campaign.brief` (tam metin; `line-clamp-2`, `--ink-700` gövde metni, turuncu değil; `v-html` yok).
- Meta satırı (`tracking-meta`, `<dl>`, ayraç " · "): segment varsa `tracking-audience-sms` / `tracking-audience-email` (`data-value` = `campaign.segment.audience.sms` / `.email`, metin `t('tracking.audience', { sms, email })` içinde); `tracking-started-at` (testid `<dd>`'de, etiket önündeki `<dt>` `t('tracking.startedAt')`; `data-value` = görünüm `sending_started_at`'i, `textContent` = `formatTsi(değer)`); `tracking-completed-at` (aynı kural, `completed_at`, `<dt>` `t('tracking.completedAt')`); değer `null` iken `<dt>`/`<dd>` çifti yok; `schedule-started-note` (§3.25 kuralı aynen) bu satırdadır.
- "Görünüm" değeri: son alınan akış olayının (`snapshot`/`status`/`completed`) alanı, akıştan henüz olay yoksa `campaign`'in alanı (§3.25 "görünüm durumu" ile aynı kaynak). Değer `null` iken öğe DOM'da yoktur.

### 3.20a Başarısız teslimat listesi (`TrackingPage`, `?status=failed`)
`delivery-failed-link` takip rotasına `?status=failed` sorgusuyla gider; `TrackingPage` bu sorgu varken `GET /api/campaigns/{id}/deliveries?status=failed&order=recent&limit=50` ile en çok **50** satır gösterir (toplam daha büyükse "ilk 50" notu). Testid'ler: `delivery-failed-list` (bölüm), her satır `delivery-failed-item` (`data-delivery-id`, `data-channel`, `data-reason` = `failure_reason`, null ise yok); toplam ilerleme çubuğu `delivery-bar-total` (`aria-valuenow` = sent+failed, `aria-valuemax` = total). Liste açılınca odak başlığına (`tabindex="-1"`), kapanınca (`Listeyi kapat`, sorgu kaldırılır) `delivery-failed-link`'e döner; bağlantı yoksa (başarısız yok veya kampanya tamamlanmadı) odak kaydırmadan `page-title`'a gider. Sorgu değişimi adım değişimi sayılmaz: `h1`'e odak taşınmaz, sayfa kaydırılmaz.

### 3.21 `ActivityFeed` (REQ-TRK-01)
Props: `items: ActivityItem[]`, `connection: ConnectionState`, `paused: boolean` (false), `newCount: number` (0). Emits: `resume()`.

```ts
interface ActivityItem { deliveryId: string; customerId: string; channel: ChannelKey; status: 'queued'|'sending'|'sent'|'failed'; attempts: number; failureReason: string | null; at: string /* Delivery.updated_at */ }
```

Testid'ler: `activity-feed` (`role="log"`, `aria-live="off"`, `data-state` — ilk eşleşen: `paused` (`paused` true) › `disconnected` (`connection` ∈ `connecting`, `error`) › `empty` (`items` boş) › `live`; `connection` `open`, `closed` (tamamlandıktan sonra) veya `idle` iken bağlantı durumu feed'i `disconnected` yapmaz), her öğe `activity-item` (`data-delivery-id`, `data-status`, `data-channel`), içinde `activity-item-customer` (textContent = `customerId`), `activity-item-time`; `empty` iken `activity-empty`; `paused` iken `activity-new-items` (metin `t('feed.newItems', { n: newCount })`). Öğelerde telefon/e-posta yoktur (API zaten vermez).

### 3.22 Hata ve geri bildirim
- `InlineError` — Props: `path: string`, `code: string`, `limit: number|null`, `actual: number|null`. Testid: `inline-error-<pathKey>` (`brief`, `sms-body`, `email-subject`, `email-body`; P9: `angle` ← `angles.a`/`angles.b`, `variant` ← `b`), `data-code`; metin `t('field_error.<code>', { limit, actual })`.
- `ErrorCard` — Props: `error: ApiError`, `action: 'retry'|'regenerate'|'new-campaign'|null`, `context: ErrorContext|null` (null). Emits: `action()`. Testid'ler: `error-card` (`data-code` = `error.code`), `error-card-message` (metin `t(errorPresentation(error, context ?? undefined).messageId)`), `error-card-action`.
- `Toaster` / bildirim: `useNotify().notify({ variant: 'success'|'info'|'warning'|'error', messageId: MessageId, params? })`. Her toast `toast` testid'i, `data-variant`, `data-message-id` taşır; `error` kalıcıdır (kapat butonu `toast-close`), diğerleri 5 sn sonra kapanır.
- `ConnectionBanner` — Props: `visible: boolean`. Testid `connection-banner` (metin `t('connection.lost')`).
- `EmptyState` — Props: `kind: 'campaign_not_found'|'not_found'|'audience_empty'|'feed_empty'`. Emits: `action()`. Testid'ler: `empty-state` (`data-kind`), `empty-state-action`.
- `Skeleton` — Props: `variant: 'pulse'|'shimmer'` (`pulse`), `class`. Testid `skeleton`, `data-variant`; her zaman `aria-hidden`; içerik iddia edilmez. `shimmer`: `--shimmer-base` iz üstünde `--shimmer-tint`/`--shimmer-peak` bandı soldan sağa (`--dur-shimmer`); reduced-motion'da sabit `--shimmer-static`. Yükseklik çağıran tarafından sabit verilir (kayma olmaması için).

### 3.23 `ContentPage` kompozisyonu (REQ-CNT-04, REQ-APR-01..03)

Sayfa; `useContentGeneration`, kampanya (`GET /api/campaigns/{id}`) ve önizleme isteklerini birleştirir. **Taban sürüm** = `latest_content_version` (varsa). Sayfa durumu `versionStatus`'tur (§5.2); bu sayfa oturumunda hiç üretim başlatılmadıysa: taban sürüm varsa `validated`, yoksa `none` sayılır.

| `versionStatus` | Gövdede görünen | Birincil eylem |
|---|---|---|
| `none` (taban sürüm yok, üretim yok) | `content-empty` (içinde `content-regenerate`, metin `t('content.generate')`, onay diyaloğu **yok**) | `content-regenerate` |
| `streaming`, `retrying` | `ChannelTabs` + her üç `StreamingText` (`content-panel-*` içinde; seçili olmayan panel `hidden`); `ContentEditor` yok | — (`content-regenerate` `aria-disabled`) |
| `interrupted` | `ChannelTabs` + üç `StreamingText` (`interrupted`) | `content-regenerate` (metin `t('content.regenerate')`, diyalog yok: onaylanacak sürüm yok) |
| `invalid` | `ChannelTabs` + üç `StreamingText` (`invalid`, hatalarıyla) + `content-edit-draft` | `content-regenerate`; ikincil `content-edit-draft` |
| `validated` | `ChannelTabs` + seçili kanalın `ContentEditor`'ı (`mode` §3.14) + `CharCounter`'lar + önizleme (`sms` sekmesinde `SmsPreview`, `email` sekmesinde `EmailPreview`) | `content-to-approval` |
| düzenleme (`content-edit-draft` sonrası) | `invalid` taslağın metinleriyle dolu `ContentEditor` (`editable`, `dirty` = true, `fieldErrors` = son `invalid` olayının hataları), `StreamingText`'ler kaldırılır; `versionStatus` `invalid` kalır, kaydedilince `validated` olur | `editor-save` |

- Her zaman görünür: `StepHeader` (`step="content"`; `step-header-status` `kind="content"`: `none` → yok, `streaming`, kaydedilmemiş düzenleme ve `content-edit-draft` sonrası → `draft`, aksi `versionStatus`; `step-header-next` sıradaki eylemi `t('content.next.<generate|streaming|invalid|interrupted|dirty|toApproval>')` ile söyler, kampanya `locked` iken yok), `page-title`, `content-live-status` (görsel olarak gizli, `role="status"`, `aria-live="polite"`; metni yalnız faz değişiminde `t('content.live.<streaming|retrying|validated|invalid|interrupted>')` olur; delta başına güncellenmez), `ConfirmDialog kind="regenerate"`.
- Düzen (görsel, DESIGN.md §8.3): sol kart editör, sağda kesik çizgili "çalışma önizlemesi" (`content.previewTitle`, `content.previewHint`); altta tek eylem şeridi (`role="group"`, `aria-label` = `t('content.actions')`): solda `content-regenerate` / `content-edit-draft`, sağda durum notu ve `content-to-approval` (metin `t('content.toApproval')` = "Onaya geç"). Bu sayfada onay/gönderim kontrolü yoktur; `content-to-approval` yalnız etkinken `primary`, `editor-save` yalnız kaydedilebilirken `primary` görünür (aynı anda tek etkin birincil buton).
- **Dil sekmeleri (P8, REQ-LNG-04, §3.27):** taban sürümde veya akışta İngilizce sürüm varsa (`latest_content_version.content.en` ≠ yok **ya da** akışta `language: "en"` parçası geldi) `LanguageTabs` (`language-tabs`) `ChannelTabs`'ın üstünde görünür; yoksa DOM'da bulunmaz ve sayfa P7'deki gibi çalışır. `streaming`/`retrying`: her dilin kendi üç `StreamingText`'i vardır; seçili olmayan dilin panelleri `hidden`. `validated`: `ContentEditor` seçili `language` için bağlanır, `CharCounter`'lar o dilin alanlarını gösterir. Hata içeren dilin sekmesi `data-has-error="true"` olur (ilgili dilin herhangi bir alanında `fieldErrors`/akış hatası).
- **A/B (P9, REQ-ABT-04, §3.28–§3.29):** eylem şeridinde `content-regenerate`'in yanında `AbToggle` `content-ab-toggle` (başlangıç: `ab=1` sorgusu varsa açık, yoksa `latest_content_version.content.b` varsa açık, aksi kapalı; `content-regenerate` kullanılamazken, yani akış sürerken veya kampanya `scheduled`/`sending`/`completed` iken, `aria-disabled`). Taban sürümde veya akışta B varsa (`latest_content_version.content.b` var **ya da** akışta `variant: "b"` parçası geldi) `VariantTabs` (`content-variant-tabs`) `LanguageTabs`'ın (yoksa `ChannelTabs`'ın) üstünde görünür; yoksa DOM'da yoktur ve sayfa P8'deki gibi çalışır. Görünür hiyerarşi: varyant → dil → kanal. `streaming`/`retrying`: her varyant × dilin kendi üç `StreamingText`'i vardır (seçili olmayanların panelleri `hidden`). `validated`: `ContentEditor` seçili `variant` + `language` için bağlanır. Sekmelerin altında seçili varyantın açısı `content-variant-angle` (`textContent` = `angles[seçili]`; açı henüz yoksa, yani akış sırasında, öğe DOM'da yoktur: açılar akmaz, `validated` ile gelir); `validated` iken düzenleme `ContentEditor`'daki `editor-angle` ile yapılır. Hata içeren varyantın sekmesi `data-has-error="true"` (o varyantın herhangi bir dil/alan hatası; B için ayrıca yol `b` ve `angles.b`, A için `angles.a`). **`invalid` durumunda** (editör yok, alanlar `StreamingText`) `useContentGeneration().variantErrors` sayfa düzeyinde, `VariantTabs`'ın hemen altında, seçili sekmeye göre süzülerek gösterilir: `inline-error-variant` (yol `b`; yalnız B seçiliyken) ve `inline-error-angle` (`angles.a` yalnız A, `angles.b` yalnız B seçiliyken), `InlineError` bileşeniyle, `data-code` ile; hata yoksa DOM'da yoktur. `content-edit-draft` ile düzenlemeye geçilince bu sayfa düzeyi öğeler kalkar, aynı hatalar `ContentEditor.fieldErrors` üzerinden editördeki aynı testid'lerde görünür (§3.14). **Taslağın açıları:** `content-edit-draft` taslağı `ContentEditor`'a yüklerken `versionId` (son `invalid` olayının `content_version_id`'si) doluysa açıları `GET /api/campaigns/{id}/content-versions/{versionId}` yanıtının `content.angles`'ından alır (anahtar yoksa `{a:'', b:''}`); `versionId` `null` ise açılar `{a:'', b:''}` ile başlar (kayıtta boş açı API'de `required` olur). Metin alanları bugünkü gibi akış taslağından gelir. Başlangıç sekmesi rota sorgusu `variant` (`a`|`b`; yok/geçersiz/`b` yokken `a`); sekme değişince `router.replace` ile güncellenir; `content-to-approval` `variant` ve `lang`'ı taşır.
- `ChannelTabs.phases`: akış durumu varsa `useContentGeneration().fields[*].phase`; yoksa (taban sürüm) üç alan `validated`. Seçili kanal varsayılanı `sms`.
- `content-regenerate` ve `StreamingText`'in `regenerate` emit'i aynı eylemdir: `validated` bir sürüm var **veya** editör `dirty` ise önce `ConfirmDialog kind="regenerate"` (kaydedilmemiş düzenleme kaybolur); onayda `start(campaignId)`.
- **Taban sürüm (`expectedVersion`):** sayfa, düzenlemenin dayandığı sürümü tutar = son bilinen `latest_content_version?.id ?? 'none'`; kampanya çekilince, `validated` olayında ve başarılı kayıtta güncellenir; `invalid` taslak düzenlenirken değişmez (taslak sürüm sayılmaz). Her kayıt isteği `X-Expected-Version: <expectedVersion>` başlığıyla gider.
- **Kaydetme:** `editor-save` → `POST /api/campaigns/{id}/content-versions` (tam içerik, `X-Expected-Version` başlığıyla). Başarıda: taban := yanıtın `content`'i, `dirty` false, `fieldErrors` boş, `worstCase` := yanıtın `worst_case`'i, `versionId` := yanıtın `id`'si, `versionStatus` `validated`, kampanya yeniden çekilir (durum `content_ready`, stepper güncellenir), önizleme yeniden istenir, `toast.contentSaved` (success). **422:** `ApiError.fields` yollarına göre `ContentEditor.fieldErrors`'a dağıtılır, editör `dirty` kalır, odak ilk hatalı alana gider. **409 `content_edit_conflict`:** kampanya yeniden çekilir; `latest_content_version.content` editördeki metne **birebir** eşitse (başarılı bir isteğin tekrarı veya çift tıklama) çakışma gösterilmez, başarıymış gibi ele alınır (taban := o sürüm, `dirty` false, `toast.contentSaved`). Aksi halde `ContentEditor.conflict` := `error.current_version`, kullanıcı metni korunur, `editor-conflict` açılır. `editor-conflict-reload` (`reload-latest`): editör metni ve taban en son sürümle değiştirilir, `dirty` false, çakışma kapanır, önizleme yeniden istenir. `editor-conflict-keep-mine` (`keep-mine`): kullanıcı metni **korunur**, taban en son sürüme çekilir (`expectedVersion` := o sürümün kimliği), `dirty` true kalır, çakışma kapanır; sonraki kayıt başkasının sürümünü geçmişte bırakarak yeni bir sürüm olarak üstüne yazar (öteki sürüm silinmez; bu "son yazan kazanır" davranışı sahibin kararıdır ve uyarı `editor-conflict-warning` ile kullanıcıya baştan söylenir, onay penceresi yoktur). **Diğer 409** (`invalid_state`, `generation_in_progress`): toast + kampanya yeniden çekilir, düzenleme korunur. **422** başlık hatası istemci hatasıdır (`header.*`): toast (error), normalde oluşmaz. **413 / ağ hatası:** §4.
- `ContentEditor.mode`: kampanya `scheduled`/`sending`/`completed` ise `locked` (ayrıca `content-regenerate`, `content-edit-draft`, `editor-save` `aria-disabled`), aksi `editable`. Editör akış sürerken bağlı olmadığından §3.14'teki "akış sürerken `readonly`" kuralı yalnız bileşen tek başına test edilirken geçerlidir.
- İstek hataları: `start()` sonrası `useContentGeneration().error` doluysa §4'e göre sunulur; `llm_unavailable` (stream açılmadan) → `ErrorCard action="regenerate"` (`error-card-action` `start()`'ı yeniden çağırır), 409'lar → toast.

### 3.24 `ApprovalPage` kompozisyonu (REQ-APR-01, -03, REQ-CNT-05)

- Mount'ta kampanya çekilir; onaylanacak sürüm **her zaman `campaign.latest_content_version`**'dır (yoksa `ApprovalBar` `versionStatus="none"`, `approval-reason` `no_content`). Önizleme §1.1'e göre **bir kez** istenir (`sample_index` 0); bu sayfada örnek gezme yoktur.
- Düzen (P6 yeniden tasarım, DESIGN.md §8.4): ortalı tek sütun (maks. 768 px), salt okunur **gönderim özeti** kartı ve kartın sonunda karar paneli. Düzenleme arayüzü (`ChannelTabs`, `ContentEditor`, `CharCounter`, değişken chip'leri) ve İçerik adımının önizleme bileşenleri (`SmsPreview` balonu, `EmailPreview` çerçevesi, `sms-preview-next`) bu sayfada **yoktur**. `StepHeader` (`step="approval"`, `status=null`: kampanya durumu zaten `AppHeader`'da; `step-header-next` yalnız onaylanacak sürüm yokken `t('approval.next.noContent')`), varsayılan slot boş.
- Testid'ler: `approval-summary` (`<section>`, `data-state` = kampanya durumu, yüklenirken `loading`). Özet kartı sürüm numarası göstermez; `approval-version` yoktur (sahip kararı, 2026-11-01: düzenlemeyle artan sayaç pazarlamacıya bilgi vermiyor). Ardından üç kontrol satırı (`<ol>`, her biri `h3` etiketli):
  1. **Kime** — `approval-audience-sms`, `approval-audience-email` (`data-value` = `segment.audience.sms|email`, kanal izni sonrası) + `t('approval.recipientsNote')`.
  2. **Ne gidiyor** — `approval-preview` (`data-state` = `loading|ready|error|empty`, yüklenirken `aria-busy`): `approval-preview-sample` (`t('approval.sample', { name })`; ad boşsa `t('preview.fallbackRecipient')`), `approval-preview-sms` (= `preview.sms.text`), `approval-preview-email-subject` (= `preview.email.subject`), `approval-preview-email-body` (= `preview.email.text`; `role="region"`, `tabindex="0"`, `aria-label` = `t('approval.emailBody')`, en çok 288 px, içinde kaydırılır). Hepsi metin interpolasyonu (REQ-UI-01); email HTML'i/iframe bu sayfada kullanılmaz. Hata: mesaj (`role="alert"`) + `approval-preview-retry` (önizlemeyi yeniden ister).
  3. **Uzunluk** — `approval-limit-check` (`data-state` = `pass|fail`, `data-sms` = `worst_case.sms_runes`, `data-subject` = `worst_case.subject_runes`; `worst_case` null ise şablon rune sayısı): `pass` ⇔ SMS ≤ 160 ve konu ≤ 80 (REQ-CNT-05, kişiselleştirme sonrası en kötü durum). Tek satır sonuç (`t('approval.limitCheck.pass|fail')`) + mono ayrıntı `t('approval.limitCheck.detail')`.
  Sürüm yoksa satırlar yerine `t('approval.noVersion')`. Kart sonu karar paneli `approval-decision` (opak `--surface-sunken`, üst kenarlık; tüm genişliklerde `sticky bottom-0`, kart `overflow-clip` olduğu için kart içinde yapışır: kart kaydırılırken görünümün altına iğnelenir, kart sonuna gelince yerine oturur; satırlar ve email gövdesi `scroll-margin-bottom` 192 px taşır, klavye odağı panelin altına düşmez): `ApprovalBar`, slot'unda `approval-edit` (ghost, içerik adımına götürür; metin `t('approval.edit')`; kampanya `sending`/`completed` iken gösterilmez). Ayrıca `ConfirmDialog`.
- **Dil (P8, REQ-LNG-04):** sürümde `en` varsa özet kartının başında `LanguageTabs` (§3.27; `language-tabs`, `language-tab-tr|en`); yoksa DOM'da yok. Başlangıç sekmesi rota sorgusu `lang` (`tr`|`en`; yok/geçersiz/`en` yokken `tr`); sekme değişince `router.replace` ile `lang` güncellenir. Seçili dil `approval-preview`'ı (`data-language`, önizleme isteği `?language=<seçili>`) ve `approval-limit-check`'in `data-sms`/`data-subject`'ini (İngilizcede `worst_case.en`) belirler; `data-state="pass"` ⇔ Türkçe ve (varsa) İngilizce sınırlar içinde. `preview.fallback` iken `preview-fallback-note` görünür. `approval-edit` `content` rotasına mevcut `lang`'ı taşır; `ContentPage`'in `content-to-approval` geçişi de aynı şekilde `lang`'ı taşır ve `ContentPage` mount'ta `lang` sorgusunu başlangıç sekmesi yapar.
- **A/B (P9, REQ-ABT-04):** sürümde `b` varsa özet kartının başında `VariantTabs` (§3.28; `content-variant-tabs`, `content-variant-tab-a|b`), `LanguageTabs`'ın üstünde; yoksa DOM'da yok. Başlangıç sekmesi rota sorgusu `variant` (`a`|`b`; yok/geçersiz/`b` yokken `a`); sekme değişince `router.replace` ile güncellenir; `approval-edit` mevcut `variant` ve `lang`'ı taşır. Seçili varyant + dil `approval-preview`'ı (`data-variant`, `data-language`; istek `?variant=<v>&language=<l>`) ve `approval-limit-check`'in `data-sms`/`data-subject`'ini belirler (`a`/`tr` → `worst_case`, `a`/`en` → `worst_case.en`, `b`/`tr` → `worst_case.b`, `b`/`en` → `worst_case.b.en`); `data-state="pass"` ⇔ sürümdeki **bütün** varyant × dil sürümleri SMS ≤ 160 ve konu ≤ 80 (seçili sekmeden bağımsız). Kime satırında `approval-ab-note` (metin `t('approval.abSplitNote')`: kitle ≈ %50/%50 bölünür, aynı kişi iki kanalda aynı varyantı alır) yalnız sürümde `b` varken bulunur. Seçili varyantın açısı `approval-variant-angle` (`textContent` = `angles[seçili]`).
- Kaldırılanlar (P6): `approval-limit-sms`, `approval-limit-sms-worst`, `approval-limit-email-subject`, `approval-limit-email-subject-worst` (tek `approval-limit-check`'e birleşti), iki adımlı karar listesi (`approval.decision`), sürüm kaynağı (`approval.source.*`), kart logosu, "Gidecek metin" etiketleri, ayrı düzenleme ipucu satırı (`approval.editHint*`; onaylıyken uyarı artık `ApprovalBar` notunda).
- **Onay:** `approval-approve` → `ConfirmDialog kind="approve"` → `confirm` → `POST /approve {content_version_id: latest.id}`. Başarı: kampanya yanıtla değiştirilir, diyalog kapanır, `toast.approved` (success), `ApprovalBar` `approved`. **422** (`sms_too_long_worst_case`, `subject_too_long_worst_case`): diyalog açık kalır, `error` prop'u dolar. **409 `stale_content_version`:** diyalog kapanır, kampanya yeniden çekilir, `stale=true`, toast. **Diğer 409/404:** diyalog kapanır, kampanya yeniden çekilir, toast (stepper §1 tablosuna göre güncellenir).
- **Gönderim:** `approval-send` → `ConfirmDialog kind="send"` (`counts` = `segment.audience.{sms,email}`, `allowSchedule=true`, `scheduleMinLeadSeconds`/`scheduleMaxAheadSeconds` = `GET /api/config` `limits`'ten; config yoksa 60 / 2592000) → `confirm` ise `POST /send {content_version_id: campaign.approval.content_version_id}`; `schedule(scheduledAt)` ise `POST /send {content_version_id: campaign.approval.content_version_id, scheduled_at: scheduledAt}`. Başarı (her iki kip): diyalog kapanır, `tracking` rotası; yanıt `state: "scheduled"` ise ayrıca `toast.scheduled` (success, `{ time: formatTsi(scheduled_at) }`). 409'lar onaydakiyle aynı biçimde ele alınır (`stale_content_version` → `stale=true`). 422 diyalog içinde (§3.18).
- **Planlıyken (`scheduled`, P7a):** `ApprovalBar` `scheduled` (§3.17, `scheduledAt` = `campaign.scheduled_at`); `approval-edit` gösterilmez; özet kartı aynı kalır (salt okunur). `approval-schedule-cancel` → `DELETE /api/campaigns/{id}/schedule`: başarıda kampanya yeniden çekilir (`approved`), `toast.scheduleCanceled` (info), `ApprovalBar` `approved` olur ve `approval-send` yeniden kullanılabilir. 409 `invalid_state` (gönderim başladı) veya 404: §4 toast + kampanya yeniden çekilir; durum `sending`/`completed` ise §1 tablosuna göre `tracking` rotasına geçilir. `approval-to-tracking` → `tracking` rotası.
- Onaylıyken içerik düzenlenirse (başka sekme/API) onay düşer; bu sayfa bunu yalnız bir sonraki istekteki 409 veya yeniden çekmeyle öğrenir (canlı bildirim yok).

### 3.25 `SchedulePanel` ve `TrackingPage` plan görünümü (REQ-SCH-04, P7a)

Props: `scheduledAt: string` (UTC ISO, `CampaignStatus.scheduled_at` / `Campaign.scheduled_at`), `cancelling: boolean` (false), `sendingNow: boolean` (false). Emits: `cancel()`, `send-now()`.
Testid'ler:
- `schedule-panel` (`<section>`, `aria-labelledby` = başlığı; başlık `h2` metni `t('schedule.title')`; `data-state`: `waiting` (kalan > 0) | `starting` (kalan = 0)).
- `schedule-time` — `data-value` = `scheduledAt` (aynen), metin `formatTsi(scheduledAt)` (§3.26; ör. `15 Ocak 2026 15:30 TSİ`), etiketi `t('schedule.timeLabel')`.
- `schedule-countdown` — `data-value` = `remainingSeconds(scheduledAt, Date.now())` (tamsayı saniye), mount'ta ve ardından `setInterval` ile **1000 ms**'de bir yeniden hesaplanır (testler `vi.useFakeTimers()` + `vi.advanceTimersByTime`); metin kalan ≥ 86400 iken `t('schedule.countdownDays', { d, hms })`, aksi halde `t('schedule.countdown', { hms })` (`formatCountdown`, §3.26); kalan 0 iken metin `t('schedule.starting')` ("Başlatılıyor…"), `aria-live` yok (her saniye duyurulmaz); sıfıra inişte bir kez `schedule-live-status` (görsel olarak gizli, `role="status"`) metni `t('schedule.starting')` olur. Rakamlar `tabular-nums`, `--text-accent-numeral`.
- `schedule-cancel` (Button secondary, metin `t('schedule.cancel')`; `cancelling` veya `sendingNow` iken `aria-disabled` + `cancel` emit etmez; `cancelling` iken `data-loading`).
- `schedule-send-now` (Button primary, metin `t('schedule.sendNow')`; `cancelling` veya `sendingNow` iken `aria-disabled` + `send-now` emit etmez; `sendingNow` iken `data-loading`).
Unmount'ta interval temizlenir. Görsel (DESIGN.md token'ları): kart `--surface-card`, başlık `--text-accent-heading`, `CalendarClock` ikonu `--info` tonunda; "Başlatılıyor…" `Loader` ikonu (reduced-motion'da dönmez). Turuncu yalnız rol token'larıyla.

`TrackingPage` (P7a ekleri; mevcut §3.20/§5.3 kuralları değişmez):
- **Görünüm durumu** = son alınan akış `status`'unun `state`'i, akış henüz bir şey getirmediyse `campaign.state`. Bu `scheduled` iken: `SchedulePanel` (`scheduledAt` = `status?.scheduled_at ?? campaign.scheduled_at`) `delivery-counters`'ın üstünde render edilir, `delivery-counters` `data-state="scheduled"` olur, `ActivityFeed` render edilmez. Canlı akış (`GET …/events`) bugünkü gibi açılır ve açık kalır; durum değişimi `status` olayıyla gelir (sse-events §2). Akıştan `state: "sending"` (veya `completed`) gelince panel kalkar ve sayaçlar normal görünür (`ActivityFeed` kapsam dışıdır, sahip kararı); `approved` gelirse (başka sekmede iptal) sayfa `approval` rotasına geçer.
- **İptal:** `schedule-cancel` → `cancelling = true` → `DELETE /api/campaigns/{id}/schedule`. 200: `toast.scheduleCanceled` (info), `approval` rotasına geçilir. 409 `invalid_state` / 404: §4 toast, kampanya yeniden çekilir, `cancelling = false`; worker gönderimi bu arada başlattıysa (yeniden çekilen durum `sending`/`completed`) sayfa §1 tablosuna göre `tracking`'de kalır ve sayaç görünümüne geçer.
- **Şimdi gönder:** `schedule-send-now` → `ConfirmDialog kind="send"` (`allowSchedule=false`, `counts` = `segment.audience.{sms,email}`) → `confirm` → `POST /send {content_version_id: campaign.approval.content_version_id}` (gövdede `scheduled_at` yok; plan aynı transaction'da düşer). 202: diyalog kapanır, rota değişmez; yanıtın `state`'i `sending`/`completed` ise görünüm durumu hemen ona geçer ve `sendingNow` false olur; aksi halde `sendingNow = true` akıştan `scheduled` dışı bir `state` gelene **veya** akış `stale` olana (bağlantı `open` değil) kadar kalır. 409/422: diyalog davranışı §3.24 gönderimdeki gibi.
- **Başlamış planlı kampanya:** görünüm durumu `sending`/`completed` **ve** `scheduled_at` dolu iken `schedule-started-note` görünür (P10'dan itibaren `delivery-counters`'ın üstünde ayrı satır değil, başlık bloğunun meta satırında, §3.20b; testid, `data-value`, metin ve görünürlük kuralı aynı) (metin `t('schedule.startedNote', { time: formatTsi(scheduled_at) })`, `data-value` = `scheduled_at`); `scheduled_at` null ise (anlık gönderim, "şimdi gönder") yoktur.

### 3.26 `@/lib/schedule.ts` (TSİ saat yardımcıları, saf fonksiyonlar)

```ts
export const SCHEDULE_TIME_ZONE = 'Europe/Istanbul'
// TSİ wall-clock 'YYYY-MM-DDTHH:MM' (datetime-local value) -> UTC 'YYYY-MM-DDTHH:MM:00Z'; anything else -> null.
export function toUtcIso(local: string): string | null
// UTC ISO (any RFC 3339 instant) -> TSİ 'YYYY-MM-DDTHH:MM' (seconds dropped, not rounded).
export function toTsiInput(iso: string): string
// UTC ISO -> '<day> <month name> <year> <HH>:<MM> ' + t('schedule.tz'), built from
// Intl.DateTimeFormat('tr-TR', { timeZone: 'Europe/Istanbul', day: 'numeric', month: 'long', year: 'numeric',
// hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts (separators are ours, not ICU's).
export function formatTsi(iso: string): string
// max(0, ceil((Date.parse(iso) - nowMs) / 1000))
export function remainingSeconds(iso: string, nowMs: number): number
// days = floor(s / 86400); hms = zero-padded 'HH:MM:SS' of s mod 86400
export function formatCountdown(seconds: number): { days: number; hms: string }
```

Kurallar (test edilebilir, süreç `TZ`'sinden bağımsız; dönüşüm IANA bölgesiyle yapılır, sabit `+03:00` yazılmaz): `toUtcIso('2026-01-15T15:30')` = `'2026-01-15T12:30:00Z'`; `toUtcIso('2026-01-15 15:30')`, `''`, `'2026-02-30T10:00'` = `null`; `toTsiInput('2026-01-15T12:30:45Z')` = `'2026-01-15T15:30'`; `formatTsi('2026-01-15T12:30:00Z')` = `'15 Ocak 2026 15:30 TSİ'` (`t('schedule.tz')` = `TSİ` iken); `formatTsi('2026-01-15T21:05:00Z')` = `'16 Ocak 2026 00:05 TSİ'`; `remainingSeconds('2026-01-15T12:00:01.500Z', Date.parse('2026-01-15T12:00:00Z'))` = 2; geçmiş an → 0; `formatCountdown(90061)` = `{ days: 1, hms: '01:01:01' }`, `formatCountdown(59)` = `{ days: 0, hms: '00:00:59' }`.

### 3.27 `LanguageTabs` (REQ-LNG-04, P8)
Props: `modelValue: 'tr'|'en'`, `phases: Record<'tr'|'en', ContentPhase>`, `errors: Record<'tr'|'en', boolean>`. Emits: `update:modelValue(l)`. Reka Tabs, `ChannelTabs` ile aynı kalıp: kök `data-testid="language-tabs"`, `role="tablist"`, `aria-label` = `t('tabs.languages')`; sekmeler `language-tab-tr`, `language-tab-en` (`role="tab"`, `aria-selected`, `data-status` = dilin özet fazı (herhangi bir alanı `invalid` ise `invalid`, aksi halde ilk alanın fazı), `data-has-error` = `errors[l]`); seçili sekme `tabindex="0"`, diğeri `-1`; `←`/`→` komşu sekmeye geçirir ve seçer. Sekme etiketleri `t('language.tr')` ("Türkçe") ve `t('language.en')` ("İngilizce"). Görsel: DESIGN.md `LanguageTabs`. Varsayılan seçili dil `tr`. İçerik metinleri her zaman metin düğümü/`value` olarak basılır (`v-html` yok, §6).

### 3.28 `VariantTabs` (REQ-ABT-04, P9)
Props: `modelValue: 'a'|'b'`, `phases: Record<'a'|'b', ContentPhase>`, `errors: Record<'a'|'b', boolean>`. Emits: `update:modelValue(v)`. `LanguageTabs` ile aynı kalıp (Reka Tabs): kök `data-testid="content-variant-tabs"`, `role="tablist"`, `aria-label` = `t('tabs.variants')`; sekmeler `content-variant-tab-a`, `content-variant-tab-b` (`role="tab"`, `aria-selected`, `data-status` = varyantın özet fazı, `data-has-error` = `errors[v]`); seçili sekme `tabindex="0"`, diğeri `-1`; `←`/`→` komşu sekmeye geçirir ve seçer (uçlarda sarar). Etiketler `t('variant.a')` ("Varyant A") ve `t('variant.b')` ("Varyant B"). Aynı bileşen `ContentPage` ve `ApprovalPage`'de aynı testid'lerle kullanılır. Varsayılan `a`. Görsel: DESIGN.md `VariantTabs`.

### 3.29 `AbToggle` (REQ-ABT-04, P9)
Props: `modelValue: boolean`, `disabled: boolean` (false). Emits: `update:modelValue(v)`. shadcn `Switch` (Reka): kök `role="switch"`, `aria-checked` = `"true"`|`"false"`, `aria-describedby` = ipucu metni (`t('content.abToggleHint')`: iki varyant üretilir, kitle gönderimde ≈ %50/%50 bölünür, maliyet ~2×), etiket `t('content.abToggle')` ("A/B testi"). `disabled` iken `aria-disabled="true"` ve tıklama emit etmez. Testid ebeveynden gelir: `AudiencePage`'de `audience-ab-toggle`, `ContentPage`'de `content-ab-toggle`. Anahtar yalnız **sonraki** üretimi etkiler (mevcut sürümü değiştirmez); açıkken `useContentGeneration().start(id, { ab: true })` → istek gövdesi `{"ab":true}`, kapalıyken `{}` (P8 ile aynı bayt).

### 3.30 `VariantBreakdown` (REQ-ABT-04, P9)
Props: `byVariant: VariantStatusCounts`, `angles: { a: string; b: string } | null`. Testid'ler: `variant-breakdown` (`<table>`, `<caption>` = `t('tracking.variantTitle')`), satırlar `variant-row-a`, `variant-row-b`; her satırda `variant-angle-<v>` (`textContent` = `angles[v]`, `angles` null ise "—"), ve kanal `<c>` ∈ `sms|email`, sayaç `<k>` ∈ `sent|failed|queued` için `variant-count-<v>-<c>-<k>` (`data-value` = `byVariant[v][c][k]`, metin tr-TR biçimli). Tablonun altında `variant-breakdown-note` (`role="note"`, metin `t('tracking.variantNote')`). Kazanan, oran karşılaştırması veya vurgu yoktur (sahip kararı: kazanan ölçülmez). `TrackingPage` `angles`'ı `campaign.latest_content_version?.content.angles`'tan verir (`sending`/`completed`'da içerik değişemez, bu onaylı sürümdür). Açı metni metin düğümüdür (`v-html` yok).

### 3.31 `CampaignPanel` (REQ-DSH-02, P10)
Brief giriş ekranındaki genel bakış. `BriefPage` onu yalnız `brief-new` rotasında, `BriefComposer` + `ErrorCard` + seed notunun (`brief-seed-note`, P10'da testid eklendi; metin `t('app.seedNote')`) **altında** ve yalnız brief gönderilmiyorken (`submitting` false; `AudienceLoadingPreview` görünürken yok) render eder (`v-if`; gizlenince unmount olur, zamanlayıcısı durur). Props yok; istemci `ApiClientKey`'den. Alt bileşenler serbesttir; sözleşme testid'lerdir.

**İstekler** (`request('GET', '/api/campaign-summaries', { query })`, openapi `listCampaignSummaries`):

| Bölüm | Mount'ta `query` | "Daha fazla göster" | Tazeleme (10 sn) |
|---|---|---|---|
| Planlanan (`scheduled`) | `{ state: 'scheduled', order: 'scheduled_at', limit: 5, offset: 0 }` | aynı `state`/`order`, `limit: 5`, `offset` = gösterilen satır | aynı `state`/`order`, `offset: 0`, `limit` = clamp(gösterilen, 5, 200) |
| Gönderilen (`sent`) | `{ state: 'sending,completed', order: 'recent', limit: 5, offset: 0 }` | aynı | aynı |

İki mount isteği birlikte ve composer'ı beklemeden gönderilir. **Tazeleme (tek `setTimeout`, `setInterval` değil):** her başarılı yanıttan sonra (mount, yeniden deneme, "daha fazla", tazeleme) bekleyen zamanlayıcı temizlenir ve gecikme = min(adaylar) ile yeniden kurulur; aday yoksa kurulmaz. Adaylar: (a) gösterilen satırlarda `state === 'sending'` varsa 10 000 ms; (b) Planlanan'da satır varsa en erken `scheduled_at` için `r = remainingSeconds(scheduled_at, Date.now())`, `r ≤ 3600` ise `r × 1000 + 10 000` ms. Tetiklenince iki tazeleme isteği (bir "daha fazla" isteği sürüyorsa atlanır; onun yanıtı zamanlayıcıyı yeniden kurar). Tazeleme yanıtı bölümün satırlarını, `total`'ını ve özet kartlarını yerinde değiştirir (iskelet yok, `data-state` `loading`'e dönmez); tazeleme hatası sessizdir ve zamanlayıcı kurmaz. Unmount'ta zamanlayıcı temizlenir. Kampanya başına `EventSource` açılmaz.

**Testid'ler:**
- `brief-campaign-panel` — `<section aria-labelledby>`, başlık `h2` `t('briefPanel.title')`.
- Özet kartları (`<dl>`, ≥ 1024 beş sütun, 768–1023 üç, < 768 iki): `campaign-kpi-total`, `campaign-kpi-scheduled`, `campaign-kpi-sent`, `campaign-kpi-messages-sent`, `campaign-kpi-messages-failed`; her biri sayıyı taşıyan `<dd>`, `data-value` = ham tamsayı (son başarılı yanıtın `overview`'ı: `campaigns.total`, `campaigns.scheduled`, `campaigns.sending + campaigns.completed`, `deliveries.sent`, `deliveries.failed`); hiç yanıt yokken öznitelik yok ve "—". Etiket `<dt>` `t('briefPanel.kpi.total|scheduled|sent|messagesSent|messagesFailed')`. Oran/yüzde yok (`%` karakteri yok). `messages-failed` > 0 ise sayı `--error` (etiket metni zaten "Başarısız mesaj").
- Bölüm `<s>` ∈ `scheduled|sent`: `brief-<s>-summary` (`<section>`, `data-state` `loading` › `error` › `empty` › `ready`; başlık `h3` `t('briefPanel.scheduledTitle')` / `t('briefPanel.sentTitle')` + `brief-<s>-total` (`data-value` = `total`, metin " · n")); `brief-<s>-list` (`<ul>`); `brief-<s>-empty` (`total` = 0; `t('briefPanel.scheduledEmpty')` / `t('briefPanel.sentEmpty')`); `brief-<s>-more` (`Button` secondary `sm`, metin `t('briefPanel.more')`, gösterilen < `total` iken; istek sürerken `data-loading="true"`); `brief-<s>-error` (`InlineError` benzeri satır, `data-code` = `ApiError.code`, metin `t('briefPanel.error')`) + `brief-<s>-retry` (`Button` ghost, `t('briefPanel.retry')`), bölümün son mount/"daha fazla" isteği hata verdiyse.
- Satır: `<li>` içinde tek link `campaign-summary-item` (`RouterLink` → `{ name: 'tracking', params: { campaignId } }`, `href` = `/campaigns/<id>/tracking`, `data-campaign-id`, `data-state`); içinde `campaign-summary-brief` (`textContent` = `brief.split('\n')[0]`; görsel `truncate`, < 640'ta `line-clamp-2`). Planlanan satırı: `campaign-summary-time` (`data-value` = `scheduled_at`, metin `formatTsi`), `campaign-summary-relative` (`data-value` = yükleme anında `remainingSeconds(scheduled_at, Date.now())`; metin göreli süre, ör. "2 saat sonra", `Intl.RelativeTimeFormat('tr-TR')`; saniyelik güncellenmez). Gönderilen satırı: `campaign-summary-state` (`StatusBadge kind="campaign"` `size="sm"`, `data-status`), `campaign-summary-bar` (`ProgressBar` `segmented`, `aria-valuenow` = sent + failed, `aria-valuemax` = total), `campaign-summary-sent` / `campaign-summary-failed` (`data-value`, metin `t('briefPanel.sentCounts', { sent, failed })` içinde), `campaign-summary-time` (`data-value` = `sending_started_at`, metin `formatTsi`).
- `brief-panel-status` — görsel olarak gizli `role="status"`; yalnız "daha fazla" sonrası bir kez `t('briefPanel.moreLoaded', { n })`. Tazeleme duyurulmaz.

**Davranış:** "daha fazla" yanıtındaki satırlar sona eklenir, kimliği zaten gösterilen satır atlanır; en az bir satır eklendiyse odak ilk yeni `campaign-summary-item`'a (`preventScroll` yok; satır görünür olsun), hiç eklenmediyse odak düğmede kalır ve duyuru yapılmaz. Bir bölümün hatası yalnız o bölümü etkiler; diğer bölüm, özet kartları (başarılı yanıttan) ve composer çalışmaya devam eder. Brief metni yalnız metin interpolasyonuyla (§6). < 640 px: satır iki satırlık kart (üstte brief, altta durum + sayılar/saat); Planlanan ve Gönderilen ≥ 1024 yan yana, altında alt alta; < 1024'te satır linki ve düğmeler ≥ 44 px.

## 4. Hata kodu → sunum → i18n

`@/api/errors.ts`:

```ts
export interface ApiError { status: number; code: ErrorCode | 'network_error'; message: string; fields: FieldError[]; requestId: string | null; currentVersion: { id: string | null; version_number: number | null } | null /* yalnız content_edit_conflict */ }
export type Display = 'page' | 'toast' | 'inline' | 'card' | 'banner'
export type ErrorContext = 'brief'|'audience'|'content'|'approval'|'send'|'tracking'
// context is optional; it only changes the result for 413/422 (which field) and stale_content_version
// in 'approval' (ApprovalBar stale). messageId is always `error.<code>` (or `error.network`).
export function errorPresentation(e: ApiError, context?: ErrorContext): { display: Display; messageId: MessageId; variant: 'warning'|'error' }
```

| `code` (HTTP) | `messageId` | `display` | Davranış |
|---|---|---|---|
| `campaign_not_found` (404) | `error.campaign_not_found` | `page` | `EmptyState kind="campaign_not_found"` |
| `not_found` (404) | `error.not_found` | `page` | `NotFoundPage` |
| `method_not_allowed` (405) | `error.method_not_allowed` | `toast` (error) | — (istemci hatası; normalde oluşmaz) |
| `content_version_not_found` (404) | `error.content_version_not_found` | `toast` (error) | kampanya yeniden çekilir |
| `customer_not_found` (404) | `error.customer_not_found` | `toast` (warning) | önizleme `sample_index=0`'a döner |
| `invalid_state` (409) | `error.invalid_state` | `toast` (warning) | kampanya yeniden çekilir, stepper güncellenir |
| `content_not_validated` (409) | `error.content_not_validated` | `toast` (warning) | — |
| `stale_content_version` (409) | `error.stale_content_version` | `toast` (warning) | `approval` bağlamında `ApprovalBar stale=true` |
| `content_edit_conflict` (409) | `error.content_edit_conflict` | `inline` | `content` bağlamında `ContentEditor.conflict` (`editor-conflict`, §3.14 g, §3.23); `error.current_version` `ApiError`'a `currentVersion` olarak taşınır |
| `generation_in_progress` (409) | `error.generation_in_progress` | `toast` (warning) | — |
| `payload_too_large` (413) | `error.payload_too_large` | `inline` | `inline-error-brief` (brief) veya ilgili editör alanı, `data-code="payload_too_large"` |
| `validation_failed` (422) | `error.validation_failed` | `inline` | `fields[]` `path`'e göre `inline-error-<pathKey>`'e dağıtılır; eşlenemeyen (`header.*`, `query.*`, `""`) `ErrorCard`'a; onay bağlamında `ConfirmDialog` içinde; odak ilk hatalı alana |
| `llm_invalid_output` (502) | `error.llm_invalid_output` | `card` | segment: `ErrorCard action="retry"` (brief bağlamı); içerikte bu kod HTTP olarak dönmez (son `invalid` olayı) |
| `llm_unavailable` (502) | `error.llm_unavailable` | `card` | `ErrorCard`; `action` bağlama göre: segment/`brief` → `retry`, `content` (stream açılmadan) → `regenerate` |
| `malformed_request` (400) | `error.malformed_request` | `toast` (error) | — |
| `internal_error` (500) | `error.internal_error` | `toast` (error) | — |
| `network_error` (fetch hatası / SSE kopması) | `error.network` | `banner` | `ConnectionBanner visible` |

Alan hata mesaj id'leri: `field_error.<code>` — 27 kodun her biri için (README §5.2; P9 ile `field_error.variants_identical`, `field_error.variant_not_available` eklendi): `field_error.required`, `field_error.too_long`, `field_error.invalid_value`, `field_error.invalid_json`, `field_error.schema_violation`, `field_error.truncated_output`, `field_error.no_tool_call`, `field_error.llm_unavailable`, `field_error.internal_error`, `field_error.sms_too_long`, `field_error.subject_too_long`, `field_error.email_body_too_long`, `field_error.sms_too_long_worst_case`, `field_error.subject_too_long_worst_case`, `field_error.unknown_variable`, `field_error.malformed_variable`, `field_error.unknown_field`, `field_error.unknown_operator`, `field_error.operator_not_allowed`, `field_error.type_mismatch`, `field_error.empty_group`, `field_error.max_depth_exceeded`, `field_error.max_conditions_exceeded`, `field_error.schedule_too_soon`, `field_error.schedule_too_far`, `field_error.variants_identical`, `field_error.variant_not_available`; ayrıca bilinmeyen kodlar için `field_error.unknown`.

Diğer zorunlu `MessageId`'ler: `provider.mock`, `provider.claude`, `provider.unknown`; `status.content.{draft,validated,invalid,interrupted}`; `status.delivery.{queued,sending,sent,failed}`; `status.campaign.{draft,audience_ready,content_ready,approved,scheduled,sending,completed}`; `step.{brief,audience,content,approval,tracking}`; `audience.consentNote`, `audience.empty`; `counter.worstCase`; `audience.loading`, `audience.loading.claude`; `stream.retrying`, `stream.interrupted`, `stream.waiting`, `stream.waiting.claude`; `approval.reason.{no_content,streaming,invalid,interrupted,dirty,state}`, `approval.staleNote`; `confirm.{approve,send,regenerate}.title`; `preview.emailFrameTitle`, `preview.fallbackRecipient`, `preview.sample`; `feed.empty`, `feed.newItems`; `connection.lost`; `delivery.staleNote`, `delivery.completedTitle`; tüm `error.*` (16 kod + `error.network`); `editor.conflict.{title,reload,keepMine,keepMineWarning}`; `tabs.channels`; `content.{generate,regenerate,editDraft,empty}`; `content.live.{streaming,retrying,validated,invalid,interrupted}`; `approval.version`, `approval.edit`; `toast.contentSaved`, `toast.approved`; P7a: `confirm.schedule.{title,body,confirm,audienceNote}`, `confirm.sendMode.{label,now,later}`, `confirm.scheduledAt.{label,hint}`, `schedule.{tz,title,timeLabel,countdown,countdownDays,starting,cancel,sendNow,startedNote}`, `approval.scheduledNote`, `approval.toTracking`, `toast.scheduled`, `toast.scheduleCanceled`; P8: `audience.languages`, `tabs.languages`, `language.{tr,en}`, `preview.fallbackNote`; P9: `tabs.variants`, `variant.{a,b}`, `content.abToggle`, `content.abToggleHint`, `editor.angle`, `approval.abSplitNote`, `tracking.variantTitle`, `tracking.variantNote`; P10: `briefPanel.{title,scheduledTitle,sentTitle,scheduledEmpty,sentEmpty,more,moreLoaded,error,retry,sentCounts}`, `briefPanel.kpi.{total,scheduled,sent,messagesSent,messagesFailed}`, `tracking.{overallTitle,processed,results,remaining,startedAt,completedAt,audience}`, `delivery.{live,lastUpdated,waiting}` (P10'dan itibaren zorunlu listede). `messages` nesnesi bu id'lerin her birini boş olmayan bir metinle içerir (test: id kümesi bu listeyi kapsar).

P7a metin önerileri (Türkçe; `web/src/i18n/tr.ts`'e arayüz dilimi yazar, bu sözleşme metni sabitlemez; testler `t(id)` kullanır):

| `MessageId` | Önerilen metin |
|---|---|
| `status.campaign.scheduled` | Planlandı |
| `confirm.schedule.title` | Gönderimi planla |
| `confirm.schedule.body` | Kampanya seçtiğin anda kendiliğinden gönderilir. Plan iptal edilene kadar içerik ve kitle değiştirilemez. |
| `confirm.schedule.confirm` | Planla |
| `confirm.schedule.audienceNote` | Kanal izinleri gönderim anında yeniden okunur; alıcı sayıları o ana kadar değişebilir. |
| `confirm.sendMode.label` | Ne zaman gönderilsin? |
| `confirm.sendMode.now` | Şimdi gönder |
| `confirm.sendMode.later` | İleri bir tarihte gönder |
| `confirm.scheduledAt.label` | Gönderim zamanı |
| `confirm.scheduledAt.hint` | En erken {min} dakika sonra, en geç 30 gün içinde. |
| `schedule.tz` | (Türkiye saati, UTC+3) |
| `schedule.title` | Gönderim planlandı |
| `schedule.timeLabel` | Planlanan zaman |
| `schedule.countdown` | {hms} |
| `schedule.countdownDays` | {d} gün {hms} |
| `schedule.starting` | Başlatılıyor… |
| `schedule.cancel` | Planı iptal et |
| `schedule.sendNow` | Şimdi gönder |
| `schedule.startedNote` | Planlanan: {time} |
| `approval.scheduledNote` | Planlandı: {time}. Değiştirmek için planı iptal et. |
| `approval.toTracking` | Takibe git |
| `toast.scheduled` | Gönderim {time} için planlandı. |
| `toast.scheduleCanceled` | Plan iptal edildi; kampanya onaylı durumda. |
| `field_error.schedule_too_soon` | Gönderim zamanı çok yakın ya da geçmişte. |
| `field_error.schedule_too_far` | Gönderim zamanı en fazla 30 gün sonrası olabilir. |

P9 metin önerileri (aynı kural: arayüz dilimi yazar, testler `t(id)` kullanır):

| `MessageId` | Önerilen metin |
|---|---|
| `tabs.variants` | Varyantlar |
| `variant.a` | Varyant A |
| `variant.b` | Varyant B |
| `content.abToggle` | A/B testi |
| `content.abToggleHint` | İki farklı açıdan metin üretilir; kitle gönderimde yaklaşık yarı yarıya bölünür. Üretim yaklaşık iki kat sürer. |
| `editor.angle` | Bu varyantın açısı |
| `approval.abSplitNote` | Kitle iki yarıya bölünür (≈ %50 / %50); aynı kişi iki kanalda aynı varyantı alır. |
| `tracking.variantTitle` | Varyantlara göre teslimat |
| `tracking.variantNote` | Mock provider açılma veya tıklama ölçmez; bu tablo yalnız teslimat sonucudur, kazanan belirlemez. |
| `field_error.variants_identical` | B varyantı A ile aynı; en az bir metni farklı yaz. |
| `field_error.variant_not_available` | Bu sürümde B varyantı yok. |

P10 metin önerileri (aynı kural):

| `MessageId` | Önerilen metin |
|---|---|
| `briefPanel.title` | Kampanyaların |
| `briefPanel.kpi.total` | Toplam kampanya |
| `briefPanel.kpi.scheduled` | Planlanan |
| `briefPanel.kpi.sent` | Gönderilen |
| `briefPanel.kpi.messagesSent` | Gönderilen mesaj |
| `briefPanel.kpi.messagesFailed` | Başarısız mesaj |
| `briefPanel.scheduledTitle` | Planlanan |
| `briefPanel.sentTitle` | Gönderilen |
| `briefPanel.scheduledEmpty` | Planlanmış kampanya yok. |
| `briefPanel.sentEmpty` | Henüz gönderilmiş kampanya yok. |
| `briefPanel.more` | Daha fazla göster |
| `briefPanel.moreLoaded` | {n} kampanya daha gösteriliyor. |
| `briefPanel.error` | Kampanyalar yüklenemedi. |
| `briefPanel.retry` | Tekrar dene |
| `briefPanel.sentCounts` | gönderildi {sent} · başarısız {failed} |
| `tracking.overallTitle` | Genel durum |
| `tracking.processed` | {done} / {total} mesaj işlendi |
| `tracking.results` | Sonuç |
| `tracking.remaining` | Kalan iş |
| `tracking.startedAt` | Başladı |
| `tracking.completedAt` | Bitti |
| `tracking.audience` | SMS {sms} · Email {email} |

## 5. SSE istemci davranışı

### 5.1 Ayrıştırıcı

```ts
// @/api/sse.ts
export interface SSEMessage { event: string; data: string; id: string | null }
export function createSSEParser(onMessage: (m: SSEMessage) => void): { push(chunk: string): void; end(): void }
```

Kurallar: satır sonu `\n`, `\r\n` veya `\r`; parçalar satır ortasından bölünebilir; `:` ile başlayan satır (heartbeat) yok sayılır; `event` yoksa `message`; birden çok `data:` satırı `\n` ile birleşir; boş satır mesajı tamamlar; `retry:` yok sayılır; `end()` tamamlanmamış mesajı **atar**.

### 5.2 İçerik üretimi — `useContentGeneration` (POST, fetch tabanlı)

İçerik üretimi POST olduğundan `EventSource` kullanılmaz; `fetch` (enjekte `FetchKey`) + `response.body.getReader()` + `TextDecoder` + `createSSEParser`.

```ts
export function useContentGeneration(): {
  start(campaignId: string, opts?: { ab?: boolean }): Promise<void> // P9: opts.ab => body {"ab":true}; otherwise '{}' (unchanged)
  abort(): void
  fields: Record<FieldKey, { text: string; phase: ContentPhase; errors: FieldError[] }> // reactive (Turkish main copy)
  enFields: Record<FieldKey, { text: string; phase: ContentPhase; errors: FieldError[] }> // P8: English version; stays idle/empty when the run has no `language: "en"` chunks
  hasEnglish: Ref<boolean>           // P8: true once an `en` chunk arrived or `validated.content.en` exists
  bFields: Record<FieldKey, { text: string; phase: ContentPhase; errors: FieldError[] }>   // P9: variant B, Turkish
  bEnFields: Record<FieldKey, { text: string; phase: ContentPhase; errors: FieldError[] }> // P9: variant B, English
  hasVariantB: Ref<boolean>          // P9: true once a `variant: "b"` chunk arrived or `validated.content.b` exists
  angles: Ref<{ a: string; b: string } | null> // P9: from `validated.content.angles` only (angles are not streamed)
  variantErrors: Ref<FieldError[]>   // P9: errors with path `b` (variants_identical) and `angles.a` / `angles.b`
  versionStatus: Ref<VersionStatus>
  versionId: Ref<string | null>      // validated'da sürüm, final invalid'de taslak id (olabilir null)
  worstCase: Ref<WorstCase | null>
  attempt: Ref<number>
  error: Ref<ApiError | null>        // stream açılmadan dönen JSON hata
}
```

Durum geçişleri (normatif, testler sahte `fetch` ile `ReadableStream` parçaları verir):

| Olay | Etki |
|---|---|
| `start()` | tüm alanlar `{text:'', phase:'streaming', errors:[]}`, `versionStatus='streaming'`, `attempt=1`, `error=null`; istek `POST /api/campaigns/{id}/content`, `Accept: text/event-stream` |
| Yanıt 2xx değil | gövde `ApiError` olarak `error`'a; alanlar `idle`, `versionStatus='none'` |
| `delta` | `fields[channel-field].text += text` (`data.language === 'en'` ise `enFields[channel-field]`, `hasEnglish = true`; P9: `data.variant === 'b'` ise `bFields` / `bEnFields`, `hasVariantB = true`); o alanın fazı `streaming`; `attempt = data.attempt` |
| `invalid` `final:false` | **tüm alanların (iki dilin) metni temizlenir**, fazları `retrying`, hatalar boş; sonraki `delta` (attempt 2) alanı yeniden `streaming` yapar |
| `invalid` `final:true` | `versionStatus='invalid'`; her hatanın `path`'ine göre alanın `errors`'ı dolar; tüm alanların fazı `invalid` (metin korunur); `path: ""` hataları üç alanın her birine eklenir (varsa `enFields` dahil); `en.` önekli yollar `enFields`'a, öneksizler `fields`'a düşer (P9: `b.en.` → `bEnFields`, `b.` → `bFields`, `b` ve `angles.*` → `variantErrors`; `path: ""` hataları B alanlarına da eklenir); `versionId = content_version_id` |
| `validated` | alan metinleri `data.content`'ten (`en` varsa `enFields` de; P9: `b` varsa `bFields`/`bEnFields`, `angles` ref'i) **yeniden yazılır** (delta birikimi değil, olay otoritedir), fazlar `validated`, `versionStatus='validated'`, `versionId`, `worstCase = data.worst_case` |
| Akış terminal olay olmadan biter (okuyucu `done`) veya 200 sonrası ağ hatası | terminal olmayan tüm alanlar `interrupted`, `versionStatus='interrupted'`; **otomatik yeniden bağlanma yok** (yeniden üretim kullanıcı kararıdır, DESIGN V11) |
| `abort()` / bileşen unmount | `AbortController.abort()`; alanlar `interrupted` (bileşen yaşıyorsa) |

`ApprovalBar` yalnız `versionStatus === 'validated'` iken `ready` olabilir; `interrupted` veya `invalid` iken onay butonu `aria-disabled="true"` (REQ-CNT-04).

Ek kurallar (sse-events.md §1.3.1 ile aynı):

- `start()` zaten `versionStatus === 'streaming'` iken çağrılırsa **yok sayılır** (ikinci istek gitmez). `start()` hiçbir zaman reddetmez (reject): sonuç `fields`/`versionStatus`/`error` ile bildirilir; döndürdüğü `Promise` akış bitince (terminal olay, kesilme veya hata) çözülür. `start()` sırasında `versionStatus === 'invalid' | 'interrupted' | 'validated'` ise önceki durum atılır (yeni üretim baştan başlar).
- İstek: `fetch(url, { method: 'POST', headers: { Accept: 'text/event-stream', 'Content-Type': 'application/json' }, body: '{}', signal })`; P9: `start(id, { ab: true })` iken `body: '{"ab":true}'`, aksi halde `'{}'` (bayt bayt bugünkü). Yanıt 2xx değilse gövde `ApiClient` ile **aynı** kuralla (`apiErrorFromBody`; JSON değilse veya gövde yoksa `status`'a göre genel kod, fetch hatası `network_error`) `ApiError`'a çevrilir.
- Ayrıştırıcıya gelen bilinmeyen `event` adı yok sayılır; `data` JSON değilse veya olay şemasına uymuyorsa akış "terminal olay yok" gibi ele alınır (alanlar `interrupted`, `versionStatus='interrupted'`, `error` boş).
- Terminal olaydan (`validated` / `invalid` `final:true`) sonra gelen veriler yok sayılır; gövde kapanınca `Promise` çözülür.
- `delta` içinde geçersiz (`channel`,`field`) çifti, `language` ≠ `en` değeri veya `variant` ≠ `b` değeri yok sayılır. `attempt` yalnız bilgidir (`attempt` ref'i); alan metinlerinin temizlenmesini yalnız `invalid` `final:false` tetikler.
- `abort()` sunucuya iptal gönderir (bağlantı kapanır; sunucu LLM çağrısını iptal eder ve kilidi bırakır). Unmount sonrası `useContentGeneration` hiçbir ref'i değiştirmez.

### 5.3 Canlı durum — `useCampaignStatusStream` (GET, EventSource)

```ts
export function useCampaignStatusStream(campaignId: Ref<string>): {
  status: Ref<CampaignStatus | null>
  connection: Ref<ConnectionState>
  lastUpdatedAt: Ref<string | null>   // son olayın status.updated_at'i
  completed: Ref<boolean>
  close(): void
}
```

- Bağlantı: `EventSourceFactoryKey` fabrikasıyla `/api/campaigns/{id}/events` (EventSource uyumlu GET). `connection` değerleri: `idle` (henüz başlatılmadı) → `connecting` (EventSource oluşturuldu veya tarayıcı kendi yeniden bağlanıyor) → `open` (`onopen`) → `closed` (`completed` alındı veya `close()`); `error` = EventSource `readyState === 2` ile kapandı ve istemci geri çekilme (backoff) süresini bekliyor.
- `snapshot`, `status` → `status` = `JSON.parse(data)`, `lastUpdatedAt` güncellenir.
- `completed` → `status` güncellenir, `completed=true`, `close()`, `connection='closed'`; yeniden bağlanılmaz.
- `onerror`: `completed` değilse `readyState !== 2` iken `connection='connecting'`, `readyState === 2` iken `connection='error'` (her ikisinde `DeliveryCounters` `stale`, `ActivityFeed` `disconnected`); `readyState === 2` ise istemci kendisi yeniden bağlanır (yeni EventSource oluşturunca `connecting`): bekleme 1 s, her başarısız denemede ×2, en çok 30 s (zamanlayıcılar `setTimeout`; testler `vi.useFakeTimers()`); başarılı `onopen` beklemeyi 1 s'ye sıfırlar. Yeniden bağlanınca gelen `snapshot` durumu tamamen değiştirir.
- Unmount → `close()`.

### 5.4 Etkinlik akışı — `useActivityFeed` (DESIGN §11 S4 kararı)

Akış **teslimat listesinin yoklanmasıyla** beslenir; durum SSE'si yalnız tetikleyicidir (sunucu olay başına teslimat yayını yapmaz).

```ts
export function useActivityFeed(campaignId: Ref<string>, status: Ref<CampaignStatus | null>, opts?: { capacity?: number /* 50; <768 px: 20 */ }): {
  items: Ref<ActivityItem[]>   // en yeni üstte
  paused: Ref<boolean>; newCount: Ref<number>; resume(): void
}
```

- `status` değiştiğinde ve `counts` önceki değerden farklıysa: en çok **2 sn'de bir** (sondan tetiklemeli kısma) `GET /api/campaigns/{id}/deliveries?order=recent&limit=20` çağrılır.
- Dönen her `Delivery` için anahtar `id + ':' + status + ':' + updated_at`; görülmemiş anahtarlar `ActivityItem` olarak (`at = updated_at`) başa eklenir; kapasite aşılırsa en eskiler atılır (halka tampon).
- `paused` (kullanıcı yukarı kaydırdı) iken yeni öğeler `newCount`'a eklenir, listeye `resume()`'da girer.
- `completed` olunca son bir kez çekilir, sonra yoklama durur.

## 6. Güvenlik kuralları (test edilebilir iddialar, REQ-UI-01)

1. **HTML olarak yorumlama yok:** yük `P = "<script>alert(1)</script><b>x</b>"` aşağıdaki prop'a verilerek mount edilir; iddia: hedef öğenin değeri `P`'ye eşittir, hedefin altında `querySelector('script')` ve `querySelector('b')` `null`'dır, `innerHTML` `&lt;script&gt;` içerir (textarea/input için `innerHTML` yerine `element.value === P`).

   | Bileşen | Yükün verildiği prop | Hedef testid (değer) |
   |---|---|---|
   | `StreamingText` | `text` | `streaming-text-content-<f>` (`textContent`) |
   | `ContentEditor` | `modelValue.sms.body` (`channel="sms"`), `modelValue.email.subject` / `.body` (`channel="email"`) | `editor-sms-body`, `editor-email-subject`, `editor-email-body` (`value`) |
   | `ContentEditor` (`language="en"`, P8) | `modelValue.en.sms.body`, `modelValue.en.email.subject` / `.body` | `editor-sms-body`, `editor-email-subject`, `editor-email-body` (`value`) |
   | `ContentEditor` (`variant="b"`, P9) | `modelValue.b.sms.body`, `modelValue.angles.b` | `editor-sms-body`, `editor-angle` (`value`) |
   | `VariantBreakdown` (P9) | `angles.a` | `variant-angle-a` (`textContent`) |
   | `SmsPreview` | `preview.sms.text` | `sms-preview-text` (`textContent`) |
   | `EmailPreview` | `preview.email.subject` | `email-preview-subject` (`textContent`) |
   | `ChatBubble` | `text` | `chat-bubble-text` (`textContent`) |
   | `SegmentRuleView` | `rule` = `{"all":[{"field":"city","op":"eq","value":P}]}` | `segment-rule-json` (`textContent` = `JSON.stringify(rule, null, 2)`) ve `segment-rule-chip` (`textContent` `P`'yi içerir) |
   | `ActivityFeed` | `items[0].customerId` | `activity-item-customer` (`textContent`) |
   | `CampaignPanel` (P10) | yanıttaki `items[0].brief` (sahte `ApiClient`) | `campaign-summary-brief` (`textContent`) |
   | `TrackingPage` (P10) | `campaign.brief` (sahte `ApiClient`) | `tracking-campaign-brief` (`textContent`) |

   `InlineError` metin prop'u almaz: yalnız `path`, `code`, `limit`, `actual` alır ve metni `t('field_error.<code>', …)`'dan üretir; iddiası: `code = P` verildiğinde `inline-error-<pathKey>` altında `script`/`b` öğesi yoktur ve `P` yalnız `data-code` öznitelik **değeri** olarak bulunur (bilinmeyen kod için metin `t('field_error.unknown')`).
2. `web/src` içinde `v-html` kullanılmaz (ESLint `vue/no-v-html: error`; CI lint).
3. **Email önizleme:** `email-preview-frame` bir `<iframe>`'dir; `getAttribute('sandbox') === ''` (öznitelik var, değeri boş; `allow-scripts`, `allow-same-origin` vb. yok); `getAttribute('srcdoc') === preview.email.html` (backend'in kaçışladığı HTML aynen; istemci HTML üretmez, birleştirmez); `src` özniteliği yok.
4. İstemci, API'nin `email.html` dışında hiçbir sunucu/LLM metnini HTML bağlamına koymaz.
5. Önizleme ve etkinlik akışı e-posta adresi veya telefon numarası göstermez (API bu alanları dönmez; bileşenler türetmez).

## 7. DESIGN.md §11 açık sorularına kararlar

| # | Soru | Karar |
|---|---|---|
| 1 | En uzun değerle uzunluk API'den gelecek mi? | **Evet.** `ContentVersion.worst_case {sms_runes, subject_runes}` ve `validated` olayındaki `worst_case`; `CharCounter` ikinci satırı (`char-counter-worst-<f>`) bunu gösterir. Doğrulama zaten bu uzunluğa göre yapılır (go-interfaces `content.Validate`). |
| 2 | Önizlemede örnek alıcı | Varsayılan: kuralla eşleşen kümenin müşteri kimliğine göre **ilk** üyesi (`sample_index=0`). "Sonraki örnek" kontrolü (`sms-preview-next`) `GET /preview?sample_index=(i+1) mod min(sample_count, 5)` çağırır (en çok 5 örnek, D49); serbest seçici yok. |
| 3 | Akış taşıması | İçerik üretimi **POST + fetch akış okuyucu** (§5.2); canlı durum **GET + EventSource** (§5.3). Durum akışı periyodik **sayaç özeti** taşır (olay başına teslimat değil). |
| 4 | Etkinlik akışı kaynağı | Sayaç değişiminde teslimat listesinin yoklanması (`order=recent`, en çok 2 sn'de bir), halka tampon 50 / mobil 20 (DESIGN V7) — §5.4. Satır: zaman, kanal, durum rozeti, müşteri kimliği, `attempts`, `failure_reason`. |
| 5 | Email gövdesi biçimi | **Düz metin.** Boş satır = paragraf, tek satır sonu = `<br>`; HTML'i yalnız backend üretir (`content.RenderEmailHTML`), istemci sanitize etmez. |

(Sorular 6–8 görsel/kapsam kararlarıdır; sözleşmeyi etkilemez.)

## 8. UI REQ eşlemesi

| REQ | ui.md bölümleri |
|---|---|
| REQ-AUD-03 | §3.7 `audience-count-*` (`data-value` = API `audience`), `audience-consent-note` |
| REQ-AUD-05 (UI) | §3.5 `brief-counter`, `brief-submit` pasifliği, `inline-error-brief`; §4 413/422 |
| REQ-CNT-01 / REQ-CNT-02 | §3.13 `CharCounter` (160/80, eşikler 144/72, `over-limit`), §3.14 |
| REQ-CNT-03 | §3.12 `VariableChip`, §3.15 `sms-preview-text` (yedek metin API render'ından), `sms-preview-recipient` |
| REQ-CNT-04 | §3.10 `StreamingText` fazları, §5.2 durum geçişleri, §1.1 (`start()` tetikleri, `content-regenerate`, `content-to-approval`), §3.17 kural 4–5, §3.23 (sayfa durum tablosu, `content-live-status`), §3.9 (klavye) |
| REQ-CNT-05 | §3.13 `char-counter-worst-<f>`, §3.18 `confirm-dialog-error` (`sms_too_long_worst_case`), §7 #1 |
| REQ-APR-01 | §1.1 sayfa eylemleri ve önizleme tetikleri, §3.14 `ContentEditor`, §3.15–3.16 önizleme, §3.17 `ApprovalBar`, §3.18 `ConfirmDialog` (odak/Esc), §3.23, §3.24 |
| REQ-APR-02 | §3.14 `inline-error-<f>` (422 alan hataları), §4 |
| REQ-APR-03 | §3.14 g ve §3.23 (`editor-conflict`, `expectedVersion`), §3.17 `stale` durumu, §4 `stale_content_version`, §1 adım erişimi, §3.24 (hangi sürüm onaylanır, `stale` yönetimi) |
| REQ-TRK-01 | §3.20 `DeliveryCounters`, §3.21 `ActivityFeed`, §5.3, §5.4 |
| REQ-AI-03 | §3.3 `ProviderBadge` (`data-provider="mock"`), §2 `GET /api/config` |
| REQ-UI-01 | §6 (1–5), §3.16 `email-preview-frame` |
| REQ-LNG-04 | §3.7 `audience-languages`, §3.14 `language` prop, §3.15 önizleme dili (sekmeyi izler, `preview-fallback-note`), §3.24 Onay ekranı dil sekmesi ve `lang` sorgusu, §3.23 dil sekmeleri, §3.27 `LanguageTabs`, §5.2 `enFields`/`hasEnglish`, §6 (İngilizce metin `v-html` ile basılmaz) |
| REQ-ABT-04 | §1.1 (`?generate=1&ab=1`, `start(id, { ab })`), §3.7 `audience-ab-toggle`, §3.14 `variant` prop + `editor-angle`, §3.15 `data-variant` ve `variant` sorgusu, §3.20/§3.30 `variant-breakdown`, §3.22 `inline-error-angle`/`inline-error-variant`, §3.23 varyant sekmeleri + `content-ab-toggle` + `content-variant-angle`, §3.24 Onay varyant sekmeleri + `approval-ab-note` + `approval-variant-angle` + limit denetimi, §3.28 `VariantTabs`, §3.29 `AbToggle`, §4 i18n, §5.2 (`bFields`, `bEnFields`, `hasVariantB`, `angles`, `variantErrors`), §6 |
| REQ-DSH-02 | §1 (yeni rota yok), §1.1 (`CampaignPanel` eylemleri), §3.31 `CampaignPanel` (istekler, testid'ler, tazeleme), §4 i18n P10, §6 |
| REQ-DSH-03 | §3.20 P10 (genel durum kartı, `delivery-percent`, `delivery-processed`, `delivery-results`/`-remaining`, `delivery-last-updated`, `delivery-live`, `delivery-waiting`, `delivery-percent-<c>`, başlık düzeni), §3.20b başlık bloğu, §3.25 `schedule-started-note` yeri, §4 i18n P10, §6 |
| REQ-SCH-04 | §1 tablo (`scheduled`), §1.1, §3.11, §3.14 (`locked`), §3.17 `scheduled`, §3.18 plan kipi, §3.20 `data-state="scheduled"`, §3.24 (gönderim ve planlıyken), §3.25 `SchedulePanel` + `TrackingPage`, §3.26 `@/lib/schedule.ts`, §4 i18n |

# UI sözleşmesi (Vue 3 + TypeScript)

Kapsam: REQ-AUD-03, REQ-AUD-05 (UI), REQ-CNT-01..05 (UI kısımları), REQ-APR-01..03, REQ-TRK-01, REQ-AI-03, REQ-UI-01.
Görsel dil ve ekran düzeni: kök dizindeki `DESIGN.md` (bağlayıcı değil; çelişkide bu dosya kazanır). API: [`openapi.yaml`](openapi.yaml), akışlar: [`sse-events.md`](sse-events.md).

## 0. Test kuralları ve modül yolları

- Araçlar: Vitest + Vue Test Utils + jsdom. Seçiciler **yalnızca** `[data-testid="…"]`. Durum iddiaları yalnızca bu dosyada adı geçen öznitelikler (`data-state`, `data-status`, `data-phase`, `data-kind`, `data-code`, `data-value`, `data-count`, `data-variant`, `data-provider`, `data-reason`, `aria-*`, `sandbox`, `srcdoc`, `title`) ve `textContent` üzerinden yapılır; CSS sınıfı ve görünür Türkçe metin seçici olarak kullanılmaz.
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
| `@/api/types.ts` | OpenAPI şemalarının TS karşılıkları (aynı adlar: `Campaign`, `ContentVersion`, `Preview`, `CampaignStatus`, `Delivery`, `FieldError`, `WorstCase`, `Audience`, …); içerik akışı olay tipleri: `ContentDeltaEvent`, `ContentInvalidEvent`, `ContentValidatedEvent` (openapi bileşenleriyle aynı ad) |
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

Her sayfanın `h1`'i `page-title` testid'ini taşır, `tabindex="-1"`'dir ve adım değişince odak ona taşınır.

Kampanya durumu → yönlendirme ve adım erişimi (`CampaignStepper`):

| Kampanya durumu | `/campaigns/:id` yönlendirir | Erişilebilir (tıklanabilir) adımlar | Kilitli adımlar |
|---|---|---|---|
| `draft` | `brief` | brief | audience, content, approval, tracking |
| `audience_ready` | `audience` | brief, audience, content | approval, tracking |
| `content_ready` | `content` | brief, audience, content, approval | tracking |
| `approved` | `approval` | brief, audience, content, approval | tracking |
| `sending`, `completed` | `tracking` | tüm adımlar (1–4 salt okunur: editör `locked`) | — |

Kilitli bir adımın rotasına doğrudan gidilirse yönlendirme tablosundaki rotaya yönlendirilir. Bilinmeyen kampanya (404 `campaign_not_found`) → aynı rotada `EmptyState kind="campaign_not_found"`.

Akış: Brief (`POST /api/campaigns` → `POST /segment`) → Kitle (`POST /content`, SSE) → İçerik (düzenleme `POST /content-versions`) → Onay (`POST /approve` → `POST /send`) → Takip (`GET /events` SSE + `GET /deliveries`).

Not: DESIGN.md §8'deki "görünür olduğu durumlar" tablosu ile bu tablo çelişirse **bu tablo geçerlidir** (ör. İçerik adımı `audience_ready`'de erişilebilirdir, çünkü içerik orada üretilir).

### 1.1 Sayfa eylemleri ve tetikleme kuralları

| Sayfa | Testid | Ne zaman etkin (değilse `aria-disabled="true"`, tıklama etkisiz) | Tıklayınca |
|---|---|---|---|
| `BriefPage` | `brief-submit` (§3.5) | §3.5 kuralları; yalnız `brief-new` rotasında görünür | `POST /api/campaigns` → başarılıysa `POST /api/campaigns/{id}/segment` → başarılıysa `audience` rotasına geçer. Segment 502/409 olursa sayfa `brief` rotasına geçer ve `ErrorCard` (`action="retry"`) gösterir; `error-card-action` yalnız segment isteğini tekrarlar |
| `AudiencePage` | `audience-generate-content` | kampanya `audience_ready`, `content_ready` veya `approved` **ve** `audience.matched > 0` | `latest_content_version` varsa önce `ConfirmDialog kind="regenerate"`; onaylanınca (veya sürüm yoksa doğrudan) `content` rotasına `?generate=1` sorgusuyla geçer |
| `AudiencePage` | `audience-edit-brief` | her zaman (kampanya `sending`/`completed` değilse) | API brief güncellemeyi desteklemediği için **yeni kampanya** başlatır: `brief-new` rotasına eski brief metni `BriefComposer.modelValue`'ya önceden doldurulmuş olarak geçer |
| `ContentPage` | `content-regenerate` | üretim sürmüyorken (`versionStatus !== 'streaming'`) ve kampanya `audience_ready`, `content_ready` veya `approved` | `validated` bir sürüm varsa önce `ConfirmDialog kind="regenerate"`; sonra `useContentGeneration().start(campaignId)`. Son `invalid` veya `interrupted` sonrası birincil eylemdir |
| `ContentPage` | `content-to-approval` | `versionStatus === 'validated'`, editör `dirty` değil ve kampanya `content_ready` veya `approved` | `approval` rotasına geçer |
| `ApprovalPage` | `approval-approve` / `approval-send` (§3.17) | §3.17 kuralları | `ConfirmDialog` (`approve` / `send`) → onayda `POST /approve` / `POST /send`; `send` başarılıysa `tracking` rotasına geçer |

`useContentGeneration().start()` **yalnızca** şu iki durumda çağrılır: (a) `ContentPage` mount edildiğinde rota sorgusu `generate=1` ise — bir kez, ardından sorgu `router.replace` ile kaldırılır; (b) `content-regenerate` (gerekiyorsa diyalog onayından sonra). `ContentPage` mount'ta başka hiçbir durumda üretim başlatmaz; `latest_content_version` varsa onu `validated` olarak gösterir (alan metinleri sürümden, `worstCase` sürümün `worst_case`'inden).

Önizleme isteği (`GET /api/campaigns/{id}/preview`) şu anlarda gönderilir: `ContentPage`'de `versionStatus` `validated`'a geçtiğinde ve mount'ta `latest_content_version` varsa; düzenleme kaydı (`POST /content-versions`) başarılı olduğunda; `ApprovalPage` mount'unda; `sms-preview-next` tıklandığında (`sample_index = (i+1) mod sample_count`). Akış sürerken, `invalid`/`interrupted` iken ve editör `dirty` iken önizleme istenmez (son başarılı önizleme gösterilmeye devam eder).

## 2. Kabuk

`App.vue`: `AppHeader` + `CampaignStepper` (kampanya rotalarında) + `ConnectionBanner` + `RouterView` + `Toaster`. `GET /api/config` uygulama açılışında bir kez çağrılır; sonucu `ProviderBadge`'e gider.

## 3. Bileşenler

Biçim: **Props** (ad: tip, varsayılan) · **Emits** · **Testid'ler**. Bütün bileşenler LLM/kullanıcı metnini yalnızca metin interpolasyonu (`{{ }}`/`textContent`) ile gösterir (§6).

### 3.1 `Button` (shadcn tabanlı)
Props: `variant: 'primary'|'secondary'|'ghost'|'danger-ghost'` (`primary`), `size: 'sm'|'md'|'lg'` (`md`), `loading: boolean` (false), `disabled: boolean` (false). Emits: `click` — `disabled` veya `loading` iken **emit etmez**. Kök `<button>`: `disabled` iken `aria-disabled="true"` (odaklanabilir kalır, `disabled` HTML özniteliği konmaz), `loading` iken `data-loading="true"`. Kendi testid'i yoktur; ebeveynin verdiği `data-testid` köke düşer.

### 3.2 `AppHeader`
Props: `provider: 'mock'|'claude'|null`, `providerLoading: boolean` (false), `campaignState: CampaignState|null`. Testid'ler: `app-header`; içinde `ProviderBadge` ve kampanya durumu için `StatusBadge` (`data-testid="app-header-campaign-state"`, `kind="campaign"`; `campaignState` null ise yok).

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
Props: `audience: Audience|null`, `loading: boolean`, `error: ApiError|null`. Emits: `edit-brief()`, `retry()`.
Testid'ler: `audience-card` (`data-state`: `loading` | `ready` | `empty` (matched = 0) | `error`); `ready`/`empty` iken: `audience-count-matched`, `audience-count-sms`, `audience-count-email` (`data-value` = API sayısı, metin tr-TR biçimli), `audience-bar-sms`, `audience-bar-email` (`role="progressbar"`, `aria-valuenow` = kanal sayısı, `aria-valuemax` = matched), `audience-consent-note` (her zaman, metin `t('audience.consentNote')`); `empty` iken `audience-empty`; `error` iken `ErrorCard`.

### 3.8 `SegmentRuleView`
Props: `rule: object`. Testid'ler: `segment-rule`; her koşul için bir `segment-rule-chip` (`data-field`, `data-op`; sayı = koşul sayısı); `segment-rule-json-toggle`; açıkken `segment-rule-json` (`<pre>`, textContent = `JSON.stringify(rule, null, 2)`).

### 3.9 `ChannelTabs`
Props: `modelValue: ChannelKey`, `phases: Record<FieldKey, ContentPhase>`. Emits: `update:modelValue(c: ChannelKey)`. Testid'ler: `channel-tab-sms`, `channel-tab-email` (`aria-selected`, `data-status` = kanalın özet fazı: kanalın herhangi bir alanı `invalid` ise `invalid`, aksi halde ilk alanının fazı), sekme `invalid` iken `channel-tab-error-sms` / `channel-tab-error-email`.
Erişilebilirlik ve klavye (Reka Tabs): kök `role="tablist"`, `aria-label` = `t('tabs.channels')`; her sekme `role="tab"`, `aria-controls` = `content-panel-<c>` öğesinin kimliği; seçili sekme `tabindex="0"`, diğeri `tabindex="-1"` (yuvarlanan tabindex). `←`/`→` odağı komşu sekmeye taşır ve **seçer** (otomatik etkinleştirme, uçlarda başa/sona sarar; `update:modelValue` emit eder), `Home`/`End` ilk/son sekmeye gider. Sekme içeriği `content-panel-sms` / `content-panel-email` (`role="tabpanel"`, `aria-labelledby` = ilgili sekme); seçili olmayan panel `hidden` özniteliğini taşır ama DOM'dan kaldırılmaz (akış sırasında gizli kanalın `StreamingText`'leri de dolmaya devam eder).

### 3.10 `StreamingText` (REQ-CNT-04, REQ-UI-01)
Props: `field: FieldKey`, `text: string`, `phase: ContentPhase`, `errors: FieldError[]` ([]). Emits: `regenerate()`.
Testid'ler (`<f>` = `field`): `streaming-text-<f>` (`data-phase` = `phase`, `aria-busy="true"` yalnız `streaming`/`retrying`), `streaming-text-content-<f>` (textContent = `text`), `streaming-text-badge-<f>` (`StatusBadge kind="content"`; `data-status`: `streaming`/`retrying`/`idle` → `draft`, diğerleri aynı adla `validated`/`invalid`/`interrupted`), `streaming-text-caret-<f>` (yalnız `streaming`), `streaming-text-retrying-<f>` (yalnız `retrying`), `streaming-text-interrupted-<f>` + `streaming-text-regenerate-<f>` (yalnız `interrupted`), `streaming-text-errors-<f>` (yalnız `invalid`; içinde her hata için bir `field-error`, `data-code` = hata kodu).

### 3.11 `StatusBadge`
Props: `kind: 'content'|'delivery'|'campaign'`, `status: string` (content: `draft|validated|invalid|interrupted`; delivery: `queued|sending|sent|failed`; campaign: 6 kampanya durumu), `size: 'sm'|'md'` (`md`). Kök testid'i ebeveynden gelir (varsayılan `status-badge`); `data-kind`, `data-status`; metin `t('status.<kind>.<status>')`; her zaman ikon + metin (renk tek başına değil).

### 3.12 `VariableChip` (REQ-CNT-03)
Props: `name: string`, `variant: 'insert'|'inline'|'invalid'`, `disabled: boolean` (false). Emits: `insert(name: string)` (yalnız `insert` ve `!disabled`). Testid: `variable-chip-<name>` (`data-variant`); metin `{{<name>}}`. `ContentEditor` araç çubuğunda yalnız `first_name`, `last_name`, `city` için `insert` chip'leri bulunur; düzenleme metnindeki izinsiz değişkenler (`{{password}}`) için önizleme altında `invalid` chip gösterilir.
`invalid` chip'leri yalnız sunum amaçlıdır ve istemcide düzenleme metninden türetilir (doğrulama değildir; asıl hata `inline-error-<f>` ile backend'den gelir): `\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}` ile bulunan adlardan `first_name|last_name|city` dışında kalanlar, tekilleştirilerek, önizlemenin altında bir `invalid` chip olarak sıralanır. Bozuk söz dizimi (`{{first_name`) chip üretmez; onu backend `malformed_variable` olarak bildirir.

### 3.13 `CharCounter` (REQ-CNT-01, -02, -05)
Props: `field: 'sms-body'|'email-subject'`, `text: string`, `limit: 160|80`, `worstCase: number|null` (null).
Sayım: `n = [...text.normalize('NFC')].length` (şablon metni, değişkenler yazıldığı gibi). Eşikler (DESIGN V5): `normal` n < 0.9×limit (SMS n < 144, konu n < 72) · `near-limit` 0.9×limit ≤ n ≤ limit (144–160 / 72–80) · `over-limit` n > limit.
Testid'ler (`<f>` = `field`): `char-counter-<f>` (`data-state`, `aria-live="polite"`), `char-counter-value-<f>` (metin `n/limit`, `data-count`=n), `char-counter-over-<f>` (yalnız `over-limit`, metin `−(n−limit)`), `char-counter-worst-<f>` — yalnız `worstCase !== null` iken: ikinci satır, metin `t('counter.worstCase', { n: worstCase })`, `data-count` = worstCase, `data-state` aynı eşiklerle `worstCase` üzerinden.
Kaynak: `worstCase` = API `ContentVersion.worst_case.sms_runes` / `.subject_runes` (veya `validated` olayının `worst_case`'i); kaydedilmemiş düzenlemede son kaydedilen sürümün değeri gösterilmez (null).

### 3.14 `ContentEditor` (REQ-APR-01, -02, -03)
Props: `modelValue: Content`, `channel: ChannelKey`, `mode: 'readonly'|'editable'|'locked'`, `dirty: boolean`, `saving: boolean` (false), `fieldErrors: FieldError[]` ([]), `worstCase: WorstCase|null` (null), `conflict: { currentVersionId: string | null; currentVersionNumber: number | null } | null` (null). Emits: `update:modelValue(c: Content)`, `save(c: Content)`, `reload-latest()`, `keep-mine()`.
Ayrıntılar: (a) `modelValue` **her zaman tam** `Content`'tir (iki kanal); `channel` yalnız hangi alanların gösterildiğini belirler ve `save(c)` tam içeriği gönderir (API gövdesi tam içeriktir). (b) `dirty` ebeveynindir: `modelValue`, taban içerikten (son `validated` sürümün içeriği; `invalid` taslak düzenlenirken taslak metinleri) alan alan katı dize eşitliğiyle farklıysa `true`. (c) `editor-save` ve alanlarda **Ctrl/⌘+Enter** aynı koşullarla `save` emit eder (kullanılabilirken); Enter metin alanında yeni satırdır, konu `input`'unda bir şey yapmaz. (d) Değişken chip'i tıklanınca `{{<ad>}}` **son odaklanan alanın** seçimine yazılır (seçim varsa yerini alır), imleç eklenen metnin sonuna konur, odak alana döner, `update:modelValue` emit edilir; hiç odaklanılmadıysa hedef kanalın gövde alanıdır (`sms-body` / `email-body`). (e) Her alan: kendi `fieldErrors` girdisi varsa veya sayacı `over-limit` ise `aria-invalid="true"`; `aria-describedby` alanın `char-counter-<f>` (varsa) ve `inline-error-<f>` (varsa) öğelerine işaret eder. `path: ""` alan hataları her alanın `inline-error-<f>`'ine eklenir (§5.2 ile aynı kural). (f) `saving` iken alanlar `readonly`, `editor-save` `aria-disabled`. (g) **Çakışma durumu** (`conflict` ≠ null; sunucu 409 `content_edit_conflict` döndü): `editor-conflict` görünür (`role="alert"`, `data-current-version-id` = `currentVersionId` veya boş, `data-value` = `currentVersionNumber` veya boş; metin `t('editor.conflict.title', { n })`), içinde `editor-conflict-reload` (metin `t('editor.conflict.reload')`, tıklayınca `reload-latest` emit) ve `editor-conflict-keep-mine` (metin `t('editor.conflict.keepMine')`, `keep-mine` emit, `aria-describedby` = `editor-conflict-warning`'in kimliği). Bu ikisinin altında, düğmeye basılmadan **her zaman görünen** açık bir uyarı vardır: `editor-conflict-warning` (metin `t('editor.conflict.keepMineWarning')`; önerilen metin: "Metnini korursan diğer sekmedeki değişiklik geçmişte kalır, yeni sürüm senin metnin olur. Eski sürüm silinmez."). Çakışma sürerken `editor-save` `aria-disabled="true"` ve `save` emit etmez; alanlar düzenlenebilir ve kullanıcı metni **korunur**; `editor-conflict` açılınca odak `editor-conflict-reload`'a gider.
Testid'ler: `channel="sms"` iken `editor-sms-body` (textarea) + `CharCounter field="sms-body"`; `channel="email"` iken `editor-email-subject` (input) + `CharCounter field="email-subject"` + `editor-email-body` (textarea); `editor-save` (`mode !== 'editable'`, `!dirty` veya `saving` iken `aria-disabled="true"`); `mode` `readonly`/`locked` iken alanlarda `readonly`; `locked` iken `editor-locked-note`; insert chip'leri `variable-chip-first_name|last_name|city` (imleç konumuna ekler, `locked`/`readonly` iken `disabled`); her alan hatası için `inline-error-<f>` (`data-code`). `mode` kuralı: akış sürerken `readonly`; kampanya `sending`/`completed` ise `locked`; aksi `editable`.

### 3.15 `SmsPreview` (REQ-APR-01, REQ-CNT-03, REQ-UI-01)
Props: `preview: Preview|null`, `loading: boolean`, `error: ApiError|null`. Emits: `next-sample()`. Testid'ler: `sms-preview` (`data-state`: `loading|ready|error|empty`), `sms-preview-text` (textContent **birebir** `preview.sms.text`), `sms-preview-recipient` (textContent = `preview.customer.first_name` boşsa `t('preview.fallbackRecipient')`, aksi `first_name`), `sms-preview-sample` (metin `t('preview.sample', { i: sample_index + 1, n: sample_count })`), `sms-preview-next` (Button; `sample_count ≤ 1` iken `aria-disabled`).
`data-state` ilk eşleşen kural (hem `SmsPreview` hem `EmailPreview`): `loading` (`loading` true; önceki başarılı `preview` varsa metni yerinde kalır) › `error` (`error` ≠ null) › `ready` (`preview` ≠ null) › `empty`. `empty` ve `error` iken metin/çerçeve öğeleri (`sms-preview-text`, `email-preview-frame`, …) DOM'da bulunmaz. `error` durumunda `error.code` `customer_not_found` ise `sms-preview-next` kullanılabilir kalır ve `sample_index=0` istenir (§4).

### 3.16 `EmailPreview` (REQ-APR-01, REQ-UI-01)
Props: `preview: Preview|null`, `loading: boolean`, `error: ApiError|null`. Testid'ler: `email-preview` (`data-state`), `email-preview-subject` (textContent = `preview.email.subject`), `email-preview-frame` — `<iframe>`: `sandbox=""` (**boş değer**, hiçbir `allow-*` yok), `srcdoc` = `preview.email.html` **birebir** (istemci değiştirmez/temizlemez), `title` = `t('preview.emailFrameTitle')`, `src` özniteliği yok.

### 3.17 `ApprovalBar` (REQ-APR-01, -03, REQ-CNT-04, -05)
Props: `campaignState: CampaignState`, `versionStatus: VersionStatus`, `dirty: boolean`, `submitting: boolean` (false), `stale: boolean` (false). Emits: `approve()`, `send()`.
`stale` prop'unu `ApprovalPage` yönetir (§3.24): onay veya gönderim isteği 409 `stale_content_version` döndürünce `true` olur; kullanıcı bir sonraki onay denemesini başlatınca veya sayfadan ayrılınca `false` olur. `disabled` iken `approval-approve` `aria-describedby` ile `approval-reason` öğesine bağlıdır.
Durum (`approval-bar`'ın `data-state`'i), ilk eşleşen:
1. `campaignState` `approved` ve `!stale` → `approved`: `approval-send` görünür (Button primary), `approval-approve` yok.
2. `stale` → `stale`: `approval-stale-note` görünür; `approval-approve` `aria-disabled` değil (yeniden onay).
3. `submitting` → `submitting`.
4. `versionStatus === 'validated'` ve `!dirty` ve `campaignState === 'content_ready'` → `ready`: `approval-approve` etkin.
5. diğer → `disabled`: `approval-approve` `aria-disabled="true"`, tıklama `approve` **emit etmez**; `approval-reason` görünür, `data-reason` ∈ `no_content` (`none`), `streaming`, `invalid`, `interrupted`, `dirty`, `state` (kampanya durumu uygun değil).
Testid'ler: `approval-bar`, `approval-approve`, `approval-send`, `approval-reason`, `approval-stale-note`. Onay/gönderim her zaman önce `ConfirmDialog`'u açar (sayfa sorumluluğu).

### 3.18 `ConfirmDialog`
Props: `open: boolean`, `kind: 'approve'|'send'|'regenerate'`, `counts: { sms: number; email: number } | null` (null), `submitting: boolean` (false), `error: ApiError|null` (null). Emits: `confirm()`, `cancel()`, `update:open(v: boolean)`. Testid'ler: `confirm-dialog` (`data-kind`), `confirm-dialog-confirm`, `confirm-dialog-cancel`, `send` iken `confirm-dialog-count-sms`, `confirm-dialog-count-email` (`data-value`), `error` varsa `confirm-dialog-error` (`data-code`; 422 alan hataları içinde `field-error` öğeleri, `data-code` ör. `sms_too_long_worst_case`).
Ek testid: `confirm-dialog-title` (metin `t('confirm.<kind>.title')`). Erişilebilirlik ve davranış (Reka AlertDialog): kök `role="alertdialog"`, `aria-modal="true"`, `aria-labelledby` = `confirm-dialog-title`; açılınca odak `confirm-dialog-cancel`'a gider (geri alınamaz eylemlerde güvenli varsayılan); odak diyalog içinde döner (Tab/Shift+Tab tuzağı); `Esc` ve arka plana tıklama `cancel` emit eder ve `update:open(false)`; kapanınca odak diyaloğu açan öğeye döner. `submitting` iken `confirm-dialog-confirm` `aria-disabled="true"` ve `confirm` emit etmez, `confirm-dialog-cancel` etkindir ama `Esc`, arka plan ve iptal **kapatmaz** (`cancel` emit edilmez). `error` null değilken diyalog açık kalır ve `confirm-dialog-error` görünür; sayfa başarıda `open`'ı `false` yapar. `confirm` her tıklamada en çok bir kez emit edilir.

### 3.19 `ProgressBar` / `ProgressRow`
Props: `value: number`, `max: number`, `variant: 'single'|'segmented'`, `segments: { sent: number; failed: number; sending: number; queued: number } | null` (null), `label: string` (''). Kök `role="progressbar"`, `aria-valuenow` = `value`, `aria-valuemin="0"`, `aria-valuemax` = `max`; testid ebeveynden düşer.

### 3.20 `DeliveryCounters` (REQ-TRK-01)
Props: `status: CampaignStatus|null`, `connection: ConnectionState`, `lastUpdatedAt: string|null`. Testid'ler: `delivery-counters` (`data-state`: `completed` (status.state = completed) › `stale` (connection ≠ `open`) › `live`); kanal `<c>` ∈ `sms|email` için `delivery-card-<c>`, `delivery-bar-<c>` (`aria-valuenow` = sent+failed, `aria-valuemax` = total), `delivery-count-<c>-<k>` (`<k>` ∈ `total|queued|sending|sent|failed`, `data-value` = `status.by_channel.<c>.<k>`); toplam `delivery-count-total-<k>` (`data-value` = `status.counts.<k>`); `stale` iken `delivery-stale-note`; `completed` iken `delivery-completed-title` ve `failed > 0` ise `delivery-failed-link` (`?status=failed` filtresiyle teslimat listesine). Kampanya düzeyinde "başarısız" durumu gösterilmez.

### 3.21 `ActivityFeed` (REQ-TRK-01)
Props: `items: ActivityItem[]`, `connection: ConnectionState`, `paused: boolean` (false), `newCount: number` (0). Emits: `resume()`.

```ts
interface ActivityItem { deliveryId: string; customerId: string; channel: ChannelKey; status: 'queued'|'sending'|'sent'|'failed'; attempts: number; failureReason: string | null; at: string /* Delivery.updated_at */ }
```

Testid'ler: `activity-feed` (`role="log"`, `aria-live="off"`, `data-state` — ilk eşleşen: `paused` (`paused` true) › `disconnected` (`connection` ∈ `connecting`, `error`) › `empty` (`items` boş) › `live`; `connection` `open`, `closed` (tamamlandıktan sonra) veya `idle` iken bağlantı durumu feed'i `disconnected` yapmaz), her öğe `activity-item` (`data-delivery-id`, `data-status`, `data-channel`), içinde `activity-item-customer` (textContent = `customerId`), `activity-item-time`; `empty` iken `activity-empty`; `paused` iken `activity-new-items` (metin `t('feed.newItems', { n: newCount })`). Öğelerde telefon/e-posta yoktur (API zaten vermez).

### 3.22 Hata ve geri bildirim
- `InlineError` — Props: `path: string`, `code: string`, `limit: number|null`, `actual: number|null`. Testid: `inline-error-<pathKey>` (`brief`, `sms-body`, `email-subject`, `email-body`), `data-code`; metin `t('field_error.<code>', { limit, actual })`.
- `ErrorCard` — Props: `error: ApiError`, `action: 'retry'|'regenerate'|'new-campaign'|null`, `context: ErrorContext|null` (null). Emits: `action()`. Testid'ler: `error-card` (`data-code` = `error.code`), `error-card-message` (metin `t(errorPresentation(error, context ?? undefined).messageId)`), `error-card-action`.
- `Toaster` / bildirim: `useNotify().notify({ variant: 'success'|'info'|'warning'|'error', messageId: MessageId, params? })`. Her toast `toast` testid'i, `data-variant`, `data-message-id` taşır; `error` kalıcıdır (kapat butonu `toast-close`), diğerleri 5 sn sonra kapanır.
- `ConnectionBanner` — Props: `visible: boolean`. Testid `connection-banner` (metin `t('connection.lost')`).
- `EmptyState` — Props: `kind: 'campaign_not_found'|'not_found'|'audience_empty'|'feed_empty'`. Emits: `action()`. Testid'ler: `empty-state` (`data-kind`), `empty-state-action`.
- `Skeleton` — testid `skeleton`; içerik iddia edilmez.

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

- Her zaman görünür: `page-title`, `content-live-status` (görsel olarak gizli, `role="status"`, `aria-live="polite"`; metni yalnız faz değişiminde `t('content.live.<streaming|retrying|validated|invalid|interrupted>')` olur; delta başına güncellenmez), `ConfirmDialog kind="regenerate"`.
- `ChannelTabs.phases`: akış durumu varsa `useContentGeneration().fields[*].phase`; yoksa (taban sürüm) üç alan `validated`. Seçili kanal varsayılanı `sms`.
- `content-regenerate` ve `StreamingText`'in `regenerate` emit'i aynı eylemdir: `validated` bir sürüm var **veya** editör `dirty` ise önce `ConfirmDialog kind="regenerate"` (kaydedilmemiş düzenleme kaybolur); onayda `start(campaignId)`.
- **Taban sürüm (`expectedVersion`):** sayfa, düzenlemenin dayandığı sürümü tutar = son bilinen `latest_content_version?.id ?? 'none'`; kampanya çekilince, `validated` olayında ve başarılı kayıtta güncellenir; `invalid` taslak düzenlenirken değişmez (taslak sürüm sayılmaz). Her kayıt isteği `X-Expected-Version: <expectedVersion>` başlığıyla gider.
- **Kaydetme:** `editor-save` → `POST /api/campaigns/{id}/content-versions` (tam içerik, `X-Expected-Version` başlığıyla). Başarıda: taban := yanıtın `content`'i, `dirty` false, `fieldErrors` boş, `worstCase` := yanıtın `worst_case`'i, `versionId` := yanıtın `id`'si, `versionStatus` `validated`, kampanya yeniden çekilir (durum `content_ready`, stepper güncellenir), önizleme yeniden istenir, `toast.contentSaved` (success). **422:** `ApiError.fields` yollarına göre `ContentEditor.fieldErrors`'a dağıtılır, editör `dirty` kalır, odak ilk hatalı alana gider. **409 `content_edit_conflict`:** kampanya yeniden çekilir; `latest_content_version.content` editördeki metne **birebir** eşitse (başarılı bir isteğin tekrarı veya çift tıklama) çakışma gösterilmez, başarıymış gibi ele alınır (taban := o sürüm, `dirty` false, `toast.contentSaved`). Aksi halde `ContentEditor.conflict` := `error.current_version`, kullanıcı metni korunur, `editor-conflict` açılır. `editor-conflict-reload` (`reload-latest`): editör metni ve taban en son sürümle değiştirilir, `dirty` false, çakışma kapanır, önizleme yeniden istenir. `editor-conflict-keep-mine` (`keep-mine`): kullanıcı metni **korunur**, taban en son sürüme çekilir (`expectedVersion` := o sürümün kimliği), `dirty` true kalır, çakışma kapanır; sonraki kayıt başkasının sürümünü geçmişte bırakarak yeni bir sürüm olarak üstüne yazar (öteki sürüm silinmez; bu "son yazan kazanır" davranışı sahibin kararıdır ve uyarı `editor-conflict-warning` ile kullanıcıya baştan söylenir, onay penceresi yoktur). **Diğer 409** (`invalid_state`, `generation_in_progress`): toast + kampanya yeniden çekilir, düzenleme korunur. **422** başlık hatası istemci hatasıdır (`header.*`): toast (error), normalde oluşmaz. **413 / ağ hatası:** §4.
- `ContentEditor.mode`: kampanya `sending`/`completed` ise `locked` (ayrıca `content-regenerate`, `content-edit-draft`, `editor-save` `aria-disabled`), aksi `editable`. Editör akış sürerken bağlı olmadığından §3.14'teki "akış sürerken `readonly`" kuralı yalnız bileşen tek başına test edilirken geçerlidir.
- İstek hataları: `start()` sonrası `useContentGeneration().error` doluysa §4'e göre sunulur; `llm_unavailable` (stream açılmadan) → `ErrorCard action="regenerate"` (`error-card-action` `start()`'ı yeniden çağırır), 409'lar → toast.

### 3.24 `ApprovalPage` kompozisyonu (REQ-APR-01, -03, REQ-CNT-05)

- Mount'ta kampanya çekilir; onaylanacak sürüm **her zaman `campaign.latest_content_version`**'dır (yoksa `ApprovalBar` `versionStatus="none"`, `approval-reason` `no_content`). Önizleme §1.1'e göre istenir.
- Testid'ler: `approval-version` (`data-version-id` = sürüm kimliği, `data-value` = `version_number`; metin `t('approval.version', { n })`), `approval-audience-sms`, `approval-audience-email` (`data-value` = `segment.audience.sms|email`), `approval-edit` (içerik adımına götürür; metin `t('approval.edit')`), `SmsPreview`, `EmailPreview`, `ApprovalBar`, `ConfirmDialog`.
- **Onay:** `approval-approve` → `ConfirmDialog kind="approve"` → `confirm` → `POST /approve {content_version_id: latest.id}`. Başarı: kampanya yanıtla değiştirilir, diyalog kapanır, `toast.approved` (success), `ApprovalBar` `approved`. **422** (`sms_too_long_worst_case`, `subject_too_long_worst_case`): diyalog açık kalır, `error` prop'u dolar. **409 `stale_content_version`:** diyalog kapanır, kampanya yeniden çekilir, `stale=true`, toast. **Diğer 409/404:** diyalog kapanır, kampanya yeniden çekilir, toast (stepper §1 tablosuna göre güncellenir).
- **Gönderim:** `approval-send` → `ConfirmDialog kind="send"` (`counts` = `segment.audience.{sms,email}`) → `POST /send {content_version_id: campaign.approval.content_version_id}`. Başarı: `tracking` rotası. 409'lar onaydakiyle aynı biçimde ele alınır (`stale_content_version` → `stale=true`).
- Onaylıyken içerik düzenlenirse (başka sekme/API) onay düşer; bu sayfa bunu yalnız bir sonraki istekteki 409 veya yeniden çekmeyle öğrenir (canlı bildirim yok).

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

Alan hata mesaj id'leri: `field_error.<code>` — 23 kodun her biri için (README §5.2): `field_error.required`, `field_error.too_long`, `field_error.invalid_value`, `field_error.invalid_json`, `field_error.schema_violation`, `field_error.truncated_output`, `field_error.no_tool_call`, `field_error.llm_unavailable`, `field_error.internal_error`, `field_error.sms_too_long`, `field_error.subject_too_long`, `field_error.email_body_too_long`, `field_error.sms_too_long_worst_case`, `field_error.subject_too_long_worst_case`, `field_error.unknown_variable`, `field_error.malformed_variable`, `field_error.unknown_field`, `field_error.unknown_operator`, `field_error.operator_not_allowed`, `field_error.type_mismatch`, `field_error.empty_group`, `field_error.max_depth_exceeded`, `field_error.max_conditions_exceeded`; ayrıca bilinmeyen kodlar için `field_error.unknown`.

Diğer zorunlu `MessageId`'ler: `provider.mock`, `provider.claude`, `provider.unknown`; `status.content.{draft,validated,invalid,interrupted}`; `status.delivery.{queued,sending,sent,failed}`; `status.campaign.{draft,audience_ready,content_ready,approved,sending,completed}`; `step.{brief,audience,content,approval,tracking}`; `audience.consentNote`, `audience.empty`; `counter.worstCase`; `stream.retrying`, `stream.interrupted`; `approval.reason.{no_content,streaming,invalid,interrupted,dirty,state}`, `approval.staleNote`; `confirm.{approve,send,regenerate}.title`; `preview.emailFrameTitle`, `preview.fallbackRecipient`, `preview.sample`; `feed.empty`, `feed.newItems`; `connection.lost`; `delivery.staleNote`, `delivery.completedTitle`; tüm `error.*` (16 kod + `error.network`); `editor.conflict.{title,reload,keepMine,keepMineWarning}`; `tabs.channels`; `content.{generate,regenerate,editDraft,empty}`; `content.live.{streaming,retrying,validated,invalid,interrupted}`; `approval.version`, `approval.edit`; `toast.contentSaved`, `toast.approved`. `messages` nesnesi bu id'lerin her birini boş olmayan bir metinle içerir (test: id kümesi bu listeyi kapsar).

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
  start(campaignId: string): Promise<void>
  abort(): void
  fields: Record<FieldKey, { text: string; phase: ContentPhase; errors: FieldError[] }> // reactive
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
| `delta` | `fields[channel-field].text += text`; o alanın fazı `streaming`; `attempt = data.attempt` |
| `invalid` `final:false` | **tüm alanların metni temizlenir**, fazları `retrying`, hatalar boş; sonraki `delta` (attempt 2) alanı yeniden `streaming` yapar |
| `invalid` `final:true` | `versionStatus='invalid'`; her hatanın `path`'ine göre alanın `errors`'ı dolar; tüm alanların fazı `invalid` (metin korunur); `path: ""` hataları üç alanın her birine eklenir; `versionId = content_version_id` |
| `validated` | alan metinleri `data.content`'ten **yeniden yazılır** (delta birikimi değil, olay otoritedir), fazlar `validated`, `versionStatus='validated'`, `versionId`, `worstCase = data.worst_case` |
| Akış terminal olay olmadan biter (okuyucu `done`) veya 200 sonrası ağ hatası | terminal olmayan tüm alanlar `interrupted`, `versionStatus='interrupted'`; **otomatik yeniden bağlanma yok** (yeniden üretim kullanıcı kararıdır, DESIGN V11) |
| `abort()` / bileşen unmount | `AbortController.abort()`; alanlar `interrupted` (bileşen yaşıyorsa) |

`ApprovalBar` yalnız `versionStatus === 'validated'` iken `ready` olabilir; `interrupted` veya `invalid` iken onay butonu `aria-disabled="true"` (REQ-CNT-04).

Ek kurallar (sse-events.md §1.3.1 ile aynı):

- `start()` zaten `versionStatus === 'streaming'` iken çağrılırsa **yok sayılır** (ikinci istek gitmez). `start()` hiçbir zaman reddetmez (reject): sonuç `fields`/`versionStatus`/`error` ile bildirilir; döndürdüğü `Promise` akış bitince (terminal olay, kesilme veya hata) çözülür. `start()` sırasında `versionStatus === 'invalid' | 'interrupted' | 'validated'` ise önceki durum atılır (yeni üretim baştan başlar).
- İstek: `fetch(url, { method: 'POST', headers: { Accept: 'text/event-stream', 'Content-Type': 'application/json' }, body: '{}', signal })`. Yanıt 2xx değilse gövde `ApiClient` ile **aynı** kuralla (`apiErrorFromBody`; JSON değilse veya gövde yoksa `status`'a göre genel kod, fetch hatası `network_error`) `ApiError`'a çevrilir.
- Ayrıştırıcıya gelen bilinmeyen `event` adı yok sayılır; `data` JSON değilse veya olay şemasına uymuyorsa akış "terminal olay yok" gibi ele alınır (alanlar `interrupted`, `versionStatus='interrupted'`, `error` boş).
- Terminal olaydan (`validated` / `invalid` `final:true`) sonra gelen veriler yok sayılır; gövde kapanınca `Promise` çözülür.
- `delta` içinde geçersiz (`channel`,`field`) çifti yok sayılır. `attempt` yalnız bilgidir (`attempt` ref'i); alan metinlerinin temizlenmesini yalnız `invalid` `final:false` tetikler.
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
   | `SmsPreview` | `preview.sms.text` | `sms-preview-text` (`textContent`) |
   | `EmailPreview` | `preview.email.subject` | `email-preview-subject` (`textContent`) |
   | `ChatBubble` | `text` | `chat-bubble-text` (`textContent`) |
   | `SegmentRuleView` | `rule` = `{"all":[{"field":"city","op":"eq","value":P}]}` | `segment-rule-json` (`textContent` = `JSON.stringify(rule, null, 2)`) ve `segment-rule-chip` (`textContent` `P`'yi içerir) |
   | `ActivityFeed` | `items[0].customerId` | `activity-item-customer` (`textContent`) |

   `InlineError` metin prop'u almaz: yalnız `path`, `code`, `limit`, `actual` alır ve metni `t('field_error.<code>', …)`'dan üretir; iddiası: `code = P` verildiğinde `inline-error-<pathKey>` altında `script`/`b` öğesi yoktur ve `P` yalnız `data-code` öznitelik **değeri** olarak bulunur (bilinmeyen kod için metin `t('field_error.unknown')`).
2. `web/src` içinde `v-html` kullanılmaz (ESLint `vue/no-v-html: error`; CI lint).
3. **Email önizleme:** `email-preview-frame` bir `<iframe>`'dir; `getAttribute('sandbox') === ''` (öznitelik var, değeri boş; `allow-scripts`, `allow-same-origin` vb. yok); `getAttribute('srcdoc') === preview.email.html` (backend'in kaçışladığı HTML aynen; istemci HTML üretmez, birleştirmez); `src` özniteliği yok.
4. İstemci, API'nin `email.html` dışında hiçbir sunucu/LLM metnini HTML bağlamına koymaz.
5. Önizleme ve etkinlik akışı e-posta adresi veya telefon numarası göstermez (API bu alanları dönmez; bileşenler türetmez).

## 7. DESIGN.md §11 açık sorularına kararlar

| # | Soru | Karar |
|---|---|---|
| 1 | En uzun değerle uzunluk API'den gelecek mi? | **Evet.** `ContentVersion.worst_case {sms_runes, subject_runes}` ve `validated` olayındaki `worst_case`; `CharCounter` ikinci satırı (`char-counter-worst-<f>`) bunu gösterir. Doğrulama zaten bu uzunluğa göre yapılır (go-interfaces `content.Validate`). |
| 2 | Önizlemede örnek alıcı | Varsayılan: kuralla eşleşen kümenin müşteri kimliğine göre **ilk** üyesi (`sample_index=0`). "Sonraki örnek" kontrolü (`sms-preview-next`) `GET /preview?sample_index=(i+1) mod sample_count` çağırır; serbest seçici yok. |
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

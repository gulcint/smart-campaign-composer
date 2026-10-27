// Every UI string lives here (DESIGN.md §10, CLAUDE.md). Placeholders: {name}.
// Ids required by docs/contracts/ui.md §4 must all stay present and non-empty.
export const tr = {
  // --- App shell ---
  'app.wordmark': 'Smart Campaign',
  'app.wordmarkAccent': 'Composer',
  'app.wordmarkShort': 'SCC',
  'app.skipToContent': 'İçeriğe geç',
  'app.seedNote': 'Gerçek kimseye mesaj gitmez; alıcılar sahte seed verisidir.',
  'app.campaignState': 'Kampanya durumu',

  // --- Provider badge (REQ-AI-03) ---
  'provider.mock': 'Mock LLM',
  'provider.claude': 'Claude',
  'provider.unknown': 'Sağlayıcı ?',
  'provider.label': 'LLM sağlayıcısı',

  // --- Status badges ---
  'status.content.draft': 'Taslak',
  'status.content.validated': 'Doğrulandı',
  'status.content.invalid': 'Geçersiz',
  'status.content.interrupted': 'Akış kesildi',
  'status.delivery.queued': 'Kuyrukta',
  'status.delivery.sending': 'Gönderiliyor',
  'status.delivery.sent': 'Gönderildi',
  'status.delivery.failed': 'Başarısız',
  'status.campaign.draft': 'Taslak',
  'status.campaign.audience_ready': 'Kitle hazır',
  'status.campaign.content_ready': 'İçerik hazır',
  'status.campaign.approved': 'Onaylandı',
  'status.campaign.sending': 'Gönderiliyor',
  'status.campaign.completed': 'Tamamlandı',

  // --- Stepper ---
  'step.brief': 'Brief',
  'step.audience': 'Kitle',
  'step.content': 'İçerik',
  'step.approval': 'Onay',
  'step.tracking': 'Takip',
  'stepper.label': 'Kampanya adımları',
  'stepper.stepNumber': 'Adım {n}',
  'stepper.compact': 'Adım {n}/{total} · {label}',
  'stepper.locked': 'Bu adım kampanya durumu nedeniyle henüz açık değil',

  // --- Brief page ---
  'brief.titleLead': 'Kampanyanı',
  'brief.titleAccent': 'tek cümleyle anlat',
  'brief.subtitle': 'Kitleyi ve mesajları Claude önerir, son kararı sen verirsin.',
  'brief.readonlyTitle': 'Kampanya brief’i',
  'brief.inputLabel': 'Kampanya brief’i',
  'brief.placeholder':
    'Örn. Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul’daki kullanıcılara %30 indirim hatırlatması gönder.',
  'brief.example': 'Örnek brief’i kullan',
  'brief.exampleText':
    'Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul’daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.',
  'brief.submit': 'Kitleyi çıkar',
  'brief.shortcut': 'Göndermek için Ctrl/⌘ + Enter',
  'brief.counterLabel': '{n} / {max} karakter',
  'brief.pending': 'Kitle çıkarılıyor…',
  'brief.retrySegment': 'Kitleyi çıkar',
  'brief.draftNote': 'Bu kampanyanın kitlesi henüz çıkarılmadı.',

  // --- Chat bubble ---
  'chat.user': 'Sen',
  'chat.assistant': 'Asistan',
  'chat.pending': 'Yanıt hazırlanıyor',

  // --- Audience ---
  'audience.title': 'Kitle',
  'audience.subtitle': 'Brief’ten çıkarılan kural ve eşleşen kişiler',
  'audience.matchedLabel': 'kişi kurala uyuyor',
  'audience.channelSms': 'SMS',
  'audience.channelEmail': 'Email',
  'audience.channelShare': '%{p}',
  'audience.barLabel': '{channel}: {count} / {total}',
  'audience.consentNote': 'Sayılar kanal izni ve iletişim bilgisi olanlara göre hesaplandı.',
  'audience.empty': 'Bu kural kimseyi seçmedi.',
  'audience.loading': 'Kitle hesaplanıyor…',
  'audience.editBrief': 'Brief’i düzenle',
  'audience.generateContent': 'İçeriği üret',
  'audience.ruleTitle': 'Segment kuralı',
  'audience.noRule': 'Henüz bir segment kuralı yok.',

  // --- Segment rule view ---
  'rule.showJson': 'JSON’u göster',
  'rule.hideJson': 'JSON’u gizle',
  'rule.not': 'değil',
  'rule.anyGroup': 'herhangi biri',
  'rule.field.city': 'Şehir',
  'rule.field.language': 'Dil',
  'rule.field.loyalty_tier': 'Sadakat seviyesi',
  'rule.field.last_cart_add_at': 'Son sepete ekleme',
  'rule.field.last_purchase_at': 'Son satın alma',
  'rule.field.orders_30d': 'Son 30 gün sipariş',
  'rule.field.cart_adds_30d': 'Son 30 gün sepete ekleme',
  'rule.field.total_orders': 'Toplam sipariş',
  'rule.field.total_spent': 'Toplam harcama',
  'rule.op.eq': '=',
  'rule.op.neq': '≠',
  'rule.op.in': '∈',
  'rule.op.not_in': '∉',
  'rule.op.gt': '>',
  'rule.op.gte': '≥',
  'rule.op.lt': '<',
  'rule.op.lte': '≤',
  'rule.op.between': 'arasında',
  'rule.op.within_last': 'son',
  'rule.op.older_than': 'daha eski:',
  'rule.op.is_null': 'yok',
  'rule.op.is_not_null': 'var',
  'rule.days': '{n} gün',
  'rule.range': '{min}–{max}',

  // --- Counters / content (P4, required ids) ---
  'counter.worstCase': 'Kişiselleştirme sonrası en fazla: {n}',
  'stream.retrying': 'Model çıktısı düzeltiliyor…',
  'stream.interrupted': 'Akış yarıda kesildi',

  // --- Approval (P4, required ids) ---
  'approval.reason.no_content': 'Henüz doğrulanmış içerik yok',
  'approval.reason.streaming': 'İçerik hâlâ üretiliyor',
  'approval.reason.invalid': 'İçerik doğrulamadan geçmedi',
  'approval.reason.interrupted': 'Akış yarıda kesildi, içeriği yeniden üret',
  'approval.reason.dirty': 'Kaydedilmemiş düzenleme var',
  'approval.reason.state': 'Kampanya durumu onaya uygun değil',
  'approval.staleNote': 'Onay düştü, yeniden onayla',

  // --- Confirm dialog ---
  'confirm.approve.title': 'İçeriği onayla',
  'confirm.send.title': 'Kampanyayı gönder',
  'confirm.regenerate.title': 'İçerik yeniden üretilsin mi?',
  'confirm.approve.body': 'Onaylanan içerik gönderime hazır hale gelir.',
  'confirm.send.body': 'Gönderim başladıktan sonra geri alınamaz.',
  'confirm.regenerate.body': 'Mevcut içerik ve varsa onay geçersiz olur.',
  'confirm.sendCounts': 'SMS {sms} · Email {email} kişiye gidecek',
  'confirm.confirm': 'Onayla',
  'confirm.cancel': 'Vazgeç',

  // --- Preview (P4, required ids) ---
  'preview.emailFrameTitle': 'Email önizlemesi',
  'preview.fallbackRecipient': 'Değerli müşterimiz',
  'preview.sample': 'Örnek {i}/{n}',

  // --- Tracking (P5, required ids) ---
  'feed.empty': 'Henüz olay yok',
  'feed.newItems': 'Yeni {n} olay',
  'delivery.staleNote': 'Bağlantı koptu; sayılar güncel olmayabilir',
  'delivery.completedTitle': 'Kampanya tamamlandı',

  // --- Connection / toasts ---
  'connection.lost': 'Bağlantı koptu, yeniden bağlanılıyor…',
  'toast.close': 'Kapat',
  'toast.region': 'Bildirimler',

  // --- Empty states / pages ---
  'empty.campaign_not_found.title': 'Kampanya bulunamadı',
  'empty.campaign_not_found.body': 'Bağlantı hatalı olabilir ya da kampanya silinmiş olabilir.',
  'empty.not_found.title': 'Sayfa bulunamadı',
  'empty.not_found.body': 'Aradığın sayfa burada değil.',
  'empty.audience_empty.title': 'Bu kural kimseyi seçmedi',
  'empty.audience_empty.body': 'Brief’i düzenleyip yeniden dene.',
  'empty.feed_empty.title': 'Henüz olay yok',
  'empty.feed_empty.body': 'Gönderim başladığında olaylar burada görünür.',
  'empty.newCampaign': 'Yeni kampanya',
  'page.content.title': 'İçerik',
  'page.approval.title': 'Onay',
  'page.tracking.title': 'Takip',
  'page.placeholder': 'Bu adım sonraki fazda geliyor.',
  'page.loading': 'Yükleniyor…',

  // --- Error card actions ---
  'errorCard.title': 'Bir sorun oluştu',
  'errorCard.action.retry': 'Tekrar dene',
  'errorCard.action.regenerate': 'Yeniden üret',
  'errorCard.action.new-campaign': 'Yeni kampanya',
  'errorCard.requestId': 'İstek kimliği: {id}',

  // --- API error messages (ui.md §4) ---
  'error.campaign_not_found': 'Kampanya bulunamadı.',
  'error.not_found': 'Sayfa bulunamadı.',
  'error.method_not_allowed': 'Bu işlem desteklenmiyor.',
  'error.content_version_not_found': 'İçerik sürümü bulunamadı; kampanya yenilendi.',
  'error.customer_not_found': 'Örnek alıcı bulunamadı; ilk örneğe dönüldü.',
  'error.invalid_state': 'Kampanya durumu değişti; ekran yenilendi.',
  'error.content_not_validated': 'İçerik doğrulanmadan bu işlem yapılamaz.',
  'error.stale_content_version': 'İçerik değişti; onayı yenilemen gerekiyor.',
  'error.generation_in_progress': 'Bu kampanya için üretim sürüyor; biraz sonra tekrar dene.',
  'error.payload_too_large': 'Metin çok büyük.',
  'error.validation_failed': 'Bazı alanlar geçersiz.',
  'error.llm_invalid_output': 'Model geçerli bir çıktı üretemedi.',
  'error.llm_unavailable': 'Modele şu anda ulaşılamıyor.',
  'error.malformed_request': 'İstek okunamadı.',
  'error.internal_error': 'Beklenmeyen bir sunucu hatası oluştu.',
  'error.network': 'Bağlantı koptu, yeniden bağlanılıyor…',

  // --- Field error messages (ui.md §4, 23 codes + unknown) ---
  'field_error.required': 'Bu alan boş bırakılamaz.',
  'field_error.too_long': 'En fazla {limit} karakter olabilir (şu an {actual}).',
  'field_error.invalid_value': 'Geçersiz değer.',
  'field_error.invalid_json': 'Model geçersiz JSON üretti.',
  'field_error.schema_violation': 'Çıktı beklenen yapıya uymuyor.',
  'field_error.truncated_output': 'Model çıktısı yarıda kesildi.',
  'field_error.no_tool_call': 'Model yapılandırılmış çıktı üretmedi.',
  'field_error.llm_unavailable': 'Modele ulaşılamadı.',
  'field_error.internal_error': 'Beklenmeyen bir hata oluştu.',
  'field_error.sms_too_long': 'SMS en fazla {limit} karakter olabilir (şu an {actual}).',
  'field_error.subject_too_long':
    'Email konusu en fazla {limit} karakter olabilir (şu an {actual}).',
  'field_error.email_body_too_long':
    'Email gövdesi en fazla {limit} karakter olabilir (şu an {actual}).',
  'field_error.sms_too_long_worst_case':
    'Kişiselleştirme sonrası SMS {limit} karakteri aşıyor (en fazla {actual}).',
  'field_error.subject_too_long_worst_case':
    'Kişiselleştirme sonrası konu {limit} karakteri aşıyor (en fazla {actual}).',
  'field_error.unknown_variable': 'Bilinmeyen değişken kullanıldı.',
  'field_error.malformed_variable': 'Değişken yazımı hatalı.',
  'field_error.unknown_field': 'Kuralda izin verilmeyen bir alan var.',
  'field_error.unknown_operator': 'Kuralda bilinmeyen bir operatör var.',
  'field_error.operator_not_allowed': 'Bu operatör bu alanla kullanılamaz.',
  'field_error.type_mismatch': 'Değerin tipi alanla uyuşmuyor.',
  'field_error.empty_group': 'Kuralda boş bir grup var.',
  'field_error.max_depth_exceeded': 'Kural çok derin iç içe.',
  'field_error.max_conditions_exceeded': 'Kuralda çok fazla koşul var.',
  'field_error.payload_too_large': 'Metin çok büyük.',
  'field_error.unknown': 'Bu alan geçersiz.',
} as const

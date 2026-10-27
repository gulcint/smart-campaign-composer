// Starts Swagger UI against the contract the API itself serves. This is an external file because
// the CSP forbids inline scripts (script-src 'self').
window.ui = SwaggerUIBundle({
  url: '/openapi.yaml',
  dom_id: '#swagger-ui',
  deepLinking: true,
  presets: [SwaggerUIBundle.presets.apis],
  layout: 'BaseLayout',
  // No call to validator.swagger.io: it would leave the origin (connect-src 'self') and leak the URL.
  validatorUrl: null,
})

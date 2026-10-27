// Generates src/api/schema.gen.ts from docs/contracts/openapi.yaml with openapi-typescript
// (contracts README §11, `api-types-check`). openapi-typescript resolves the spec's external
// $refs (schemas/*.schema.json) itself, so no bundled copy of the spec is written anywhere.
// Usage: node scripts/api-types.mjs [output]   (default src/api/schema.gen.ts)
import { writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

import openapiTS, { astToString } from 'openapi-typescript'

const spec = new URL('../../docs/contracts/openapi.yaml', import.meta.url)
const out = process.argv[2] ?? fileURLToPath(new URL('../src/api/schema.gen.ts', import.meta.url))

const header = `// GENERATED FILE — do not edit by hand. (Excluded from ESLint and Prettier.)
// Source: docs/contracts/openapi.yaml (+ docs/contracts/schemas/*.schema.json via $ref)
// Generator: openapi-typescript (version pinned in web/package.json), web/scripts/api-types.mjs
// Regenerate with \`make api-types\`; CI runs \`make api-types-check\` and fails when this is stale.
`

const ast = await openapiTS(spec)
await writeFile(out, header + '\n' + astToString(ast))

#!/usr/bin/env node
// Converts an OpenAPI 3.0 YAML file to Swagger 2.0 YAML.
// Usage: node scripts/convert-to-swagger.mjs <input> <output>

import converter from '../node_modules/api-spec-converter/index.js';
import { writeFileSync } from 'fs';

const [input, output] = process.argv.slice(2);
if (!input || !output) {
  console.error('Usage: node scripts/convert-to-swagger.mjs <input> <output>');
  process.exit(1);
}

const result = await converter.convert({ from: 'openapi_3', to: 'swagger_2', source: input });
writeFileSync(output, result.stringify({ syntax: 'yaml', order: 'default' }));

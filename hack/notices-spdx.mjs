// SPDX selection for the existing ALLOWED policy. Parsing never silently skips unknown syntax.
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const OVERRIDES = {
  // Inspected duck@0.1.12/LICENSE: copyright + exactly the two BSD redistribution clauses.
  'duck@0.1.12': { metadata: 'BSD', license: 'BSD-2-Clause' },
};
const TEXTS = {
  // Both obligations travel with the package: root LICENSE and the zlib port's full notice.
  'pako@1.0.11': { MIT: 'LICENSE', Zlib: 'lib/zlib/README' },
};

export function selectLicenses(expression, allowed) {
  const tokens = expression.match(/[A-Za-z0-9.+-]+|[()]/g) ?? [];
  if (!tokens.length || tokens.join('') !== expression.replace(/\s/g, '')) throw new Error(`Invalid SPDX expression: ${expression}`);
  let at = 0;
  function atom() {
    const token = tokens[at++];
    if (token === '(') {
      const value = or();
      if (tokens[at++] !== ')') throw new Error(`Unclosed SPDX group: ${expression}`);
      return value;
    }
    if (!token || ['AND', 'OR', 'WITH', ')'].includes(token)) throw new Error(`Invalid SPDX operand: ${expression}`);
    return { id: token };
  }
  function and() {
    let left = atom();
    while (tokens[at] === 'AND') { at++; left = { op: 'AND', left, right: atom() }; }
    return left;
  }
  function or() {
    let left = and();
    while (tokens[at] === 'OR') { at++; left = { op: 'OR', left, right: and() }; }
    return left;
  }
  const tree = or();
  if (at !== tokens.length) throw new Error(`Unsupported SPDX syntax: ${expression}`);
  function choose(node) {
    if (node.id) return allowed.has(node.id) ? [node.id] : null;
    const left = choose(node.left), right = choose(node.right);
    if (node.op === 'OR') return left ?? right;
    return left && right ? [...new Set([...left, ...right])] : null;
  }
  const selected = choose(tree);
  if (!selected) throw new Error(`No allowed SPDX alternative: ${expression}`);
  return selected;
}
export function packageLicenses(name, version, metadata, allowed) {
  const override = OVERRIDES[`${name}@${version}`];
  return selectLicenses(override?.metadata === metadata ? override.license : metadata, allowed);
}

/** One TSV entry per required licence; conjunctive obligations are never collapsed into one. */
export function normalizePackages(groups, allowed) {
  const rows = [], texts = [];
  for (const [metadata, packages] of Object.entries(groups)) {
    for (const pkg of packages) {
      if (!Array.isArray(pkg.versions) || !pkg.versions.length) throw new Error(`Missing versions for ${pkg.name}`);
      for (const version of pkg.versions) {
        const name = `${pkg.name}@${version}`;
        const selected = packageLicenses(pkg.name, version, metadata, allowed);
        for (const license of selected) rows.push([pkg.name, version, license]);
        if (selected.length > 1) {
          // Fail closed until every conjunctive text has a reviewed source in the installed package.
          const root = (pkg.paths ?? []).find((path) => JSON.parse(readFileSync(join(path, 'package.json'), 'utf8')).version === version);
          for (const license of selected) {
            const file = TEXTS[name]?.[license];
            if (!root || !file) throw new Error(`Missing reviewed ${license} text for ${name}`);
            const text = readFileSync(join(root, file), 'utf8');
            if (!text.trim()) throw new Error(`Empty ${license} text for ${name}`);
            texts.push({ name, license, file, text });
          }
        }
      }
    }
  }
  rows.sort((a, b) => a.join('\t').localeCompare(b.join('\t'), 'en'));
  texts.sort((a, b) => `${a.name}/${a.license}`.localeCompare(`${b.name}/${b.license}`, 'en'));
  return {
    tsv: rows.map((row) => row.join('\t')).join('\n') + '\n',
    markdown: texts.map(({ name, license, file, text }) => `## ${name} — ${license} text (${file})\n\n\`\`\`\n${text.trimEnd()}\n\`\`\`\n`).join('\n'),
  };
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const result = normalizePackages(JSON.parse(readFileSync(process.argv[2], 'utf8')), new Set(process.argv[3].split(' ')));
    writeFileSync(process.argv[4], result.markdown);
    process.stdout.write(result.tsv);
  } catch (error) { process.stderr.write(`notices: ${error.message}\n`); process.exitCode = 1; }
}

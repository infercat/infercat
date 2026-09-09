// SPDX selection for the existing ALLOWED policy. Parsing never silently skips unknown syntax.
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { join,posix } from 'node:path';
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

// Inventory comes only from the production lockfile closure, including every optional edge.
// pnpm's installed report supplies metadata/text paths, never membership.
export function lockedPackages(lock, installed) {
  if (String(lock.lockfileVersion) !== '9.0' || !lock.importers?.['.'] || !lock.packages || !lock.snapshots) throw new Error('Unsupported pnpm lockfile');
  const ids = new Set(), visited = new Set();
  function walk(entry, importer) {
    for (const [name, value] of Object.entries({ ...entry.dependencies, ...entry.optionalDependencies })) {
      const version = typeof value === 'string' ? value : value.version;
      if(typeof version==='string'&&version.startsWith('link:')) {
        const target=importer===undefined?'':posix.normalize(posix.join(importer,version.slice(5)));
        if(!lock.importers[target])throw new Error(`Missing locked workspace: ${target||version}`);
        const mark='workspace:'+target;if(visited.has(mark))continue;visited.add(mark);
        walk(lock.importers[target],target);continue;
      }
      const key = `${name}@${version}`, id = key.split('(')[0];
      if (visited.has(key)) continue;
      if (!lock.snapshots[key] || !lock.packages[id]) throw new Error(`Missing locked package: ${key}`);
      visited.add(key); ids.add(id); walk(lock.snapshots[key]);
    }
  }
  visited.add('workspace:.');walk(lock.importers['.'],'.');
  const metadata = new Map();
  for (const [license, packages] of Object.entries(installed)) for (const pkg of packages) {
    for (const version of pkg.versions) metadata.set(`${pkg.name}@${version}`, { ...pkg, versions: [version], license });
  }
  const groups = {}, family = '@napi-rs/canvas@1.0.8';
  let familyText;
  for (const id of [...ids].sort()) {
    let pkg = metadata.get(id);
    if (/^@napi-rs\/canvas-[a-z0-9-]+@1\.0\.8$/.test(id)) {
      const name = id.slice(0, id.lastIndexOf('@'));
      const parent = metadata.get(family);
      if (lock.snapshots[family]?.optionalDependencies?.[name] !== '1.0.8' || !ids.has(family) || parent?.license !== 'MIT') throw new Error(`Unreviewed native family: ${id}`);
      const root = parent.paths.find((path) => JSON.parse(readFileSync(join(path, 'package.json'), 'utf8')).version === '1.0.8');
      familyText = readFileSync(join(root, 'LICENSE'), 'utf8');
      // Reviewed parent LICENSE and all 11 registry package.json records at 1.0.8 (MIT, same repo).
      // Native tarballs omit LICENSE; pin the parent's text, never infer from a name alone.
      if (createHash('sha256').update(familyText).digest('hex') !== '8802fecf9da4367bc23bcf20b21cc143785fc6c92b152f3fa7fbe6ce08d344d6') throw new Error('Canvas family LICENSE changed');
      if (pkg && pkg.license !== 'MIT') throw new Error(`Canvas licence changed: ${id}`);
      for (const path of pkg?.paths ?? []) {
        const file = join(path, 'LICENSE');
        if (existsSync(file) && readFileSync(file, 'utf8') !== familyText) throw new Error(`Canvas native LICENSE differs: ${id}`);
      }
      pkg ??= { name, versions: ['1.0.8'], paths: [], license: 'MIT' };
    }
    if (!pkg) throw new Error(`No licence metadata for locked package: ${id}`);
    (groups[pkg.license] ??= []).push(pkg);
  }
  return { groups, familyText };
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
    const installed = JSON.parse(readFileSync(process.argv[2], 'utf8'));
    // Reuse the already locked ESLint dependency; no new runtime or tool dependency.
    const webRequire = createRequire(new URL('../web/package.json', import.meta.url));
    const yaml = createRequire(webRequire.resolve('eslint/package.json'))('js-yaml');
    const { groups, familyText } = lockedPackages(yaml.load(readFileSync(process.argv[5], 'utf8')), installed);
    const result = normalizePackages(groups, new Set(process.argv[3].split(' ')));
    if (familyText) result.markdown += `\n## @napi-rs/canvas native family 1.0.8 — MIT text\n\nAll locked variants are included through pdfjs-dist's optional production dependency.\nThe native packages declare MIT and the same upstream repository; their published tarballs\nomit LICENSE. Text comes from @napi-rs/canvas@1.0.8/LICENSE (pinned SHA-256); an installed\nvariant's own LICENSE, when present, must be byte-identical.\n\n\`\`\`\n${familyText.trimEnd()}\n\`\`\`\n`;

    writeFileSync(process.argv[4], result.markdown);
    process.stdout.write(result.tsv);
  } catch (error) { process.stderr.write(`notices: ${error.message}\n`); process.exitCode = 1; }
}

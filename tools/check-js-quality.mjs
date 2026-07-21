#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const root = process.cwd();
const syntaxRoots = ['public/js', 'render-service', 'tests', 'tools'];
const graphRoots = ['public/js', 'render-service'];
const extensions = new Set(['.js', '.mjs']);

function walk(relativeRoot) {
  const absoluteRoot = path.join(root, relativeRoot);
  if (!existsSync(absoluteRoot)) return [];
  const files = [];
  const visit = absolute => {
    for (const entry of readdirSync(absolute, { withFileTypes: true })) {
      if (entry.name === 'node_modules') continue;
      const candidate = path.join(absolute, entry.name);
      if (entry.isDirectory()) visit(candidate);
      else if (entry.isFile() && extensions.has(path.extname(entry.name))) files.push(candidate);
    }
  };
  visit(absoluteRoot);
  return files;
}

const syntaxFiles = new Set([
  ...syntaxRoots.flatMap(walk),
  path.join(root, 'playwright.config.mjs'),
]);
const syntaxErrors = [];
for (const filename of [...syntaxFiles].sort()) {
  try {
    execFileSync(process.execPath, ['--check', filename], { stdio: 'pipe' });
  } catch (error) {
    syntaxErrors.push(String(error.stderr || error.message).trim());
  }
}
if (syntaxErrors.length) {
  process.stderr.write(syntaxErrors.join('\n') + '\n');
  process.exit(1);
}

function resolveImport(from, specifier) {
  if (!specifier.startsWith('.')) return '';
  const raw = path.resolve(path.dirname(from), specifier);
  for (const candidate of [raw, `${raw}.js`, `${raw}.mjs`, path.join(raw, 'index.js'), path.join(raw, 'index.mjs')]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
  }
  return '';
}

const graphFiles = new Set(graphRoots.flatMap(walk));
const graph = new Map();
const staticImport = /\b(?:import|export)\s+(?:[^'";]*?\sfrom\s*)?['"]([^'"]+)['"]/g;
const dynamicImport = /\bimport\(\s*['"]([^'"]+)['"]\s*\)/g;
for (const filename of graphFiles) {
  const source = readFileSync(filename, 'utf8');
  const dependencies = new Set();
  for (const pattern of [staticImport, dynamicImport]) {
    pattern.lastIndex = 0;
    for (const match of source.matchAll(pattern)) {
      const resolved = resolveImport(filename, match[1]);
      if (resolved && graphFiles.has(resolved)) dependencies.add(resolved);
    }
  }
  graph.set(filename, [...dependencies]);
}

const visiting = new Set();
const visited = new Set();
const stack = [];
const cycles = [];
function visit(filename) {
  if (visiting.has(filename)) {
    const start = stack.indexOf(filename);
    cycles.push([...stack.slice(start), filename]);
    return;
  }
  if (visited.has(filename)) return;
  visiting.add(filename);
  stack.push(filename);
  for (const dependency of graph.get(filename) || []) visit(dependency);
  stack.pop();
  visiting.delete(filename);
  visited.add(filename);
}
for (const filename of [...graph.keys()].sort()) visit(filename);

if (cycles.length) {
  for (const cycle of cycles) {
    process.stderr.write(`import cycle: ${cycle.map(file => path.relative(root, file)).join(' -> ')}\n`);
  }
  process.exit(1);
}

process.stdout.write(`JS quality gate ok: ${syntaxFiles.size} files, ${graph.size} module graph nodes\n`);

#!/usr/bin/env bash
# Rebuilds the CodeMirror 6 bundle under web/admin/assets/codemirror/ (ticket 09, G1).
#
# usage: scripts/vendor/codemirror.sh
#
# Installs the pinned packages from the npm registry into a temporary directory
# (npm ci against the lock file written below), bundles them with esbuild into one
# IIFE that sets window.JellyCM, and writes editor.js, LICENSE and VERSION.
# Requires node and npm. Update the versions here, rerun, and commit the output.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "$0")/../.." && pwd)"
out_dir="$repo_root/web/admin/assets/codemirror"
build_date="$(date -u +%Y-%m-%d)"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

cp "$repo_root/scripts/vendor/codemirror/"{package.json,package-lock.json,entry.js} "$temp_dir/"
cd "$temp_dir"
npm ci --no-audit --no-fund --silent
npx --no-install esbuild entry.js --bundle --minify --format=iife --global-name=JellyCM \
  --target=es2019 --legal-comments=none --outfile=editor.js
mkdir -p "$out_dir"
cp editor.js "$out_dir/editor.js"
node --input-type=module - "$out_dir" "$build_date" <<'JS'
import fs from 'node:fs';
import {execFileSync} from 'node:child_process';
const [out,date]=process.argv.slice(2),lock=JSON.parse(fs.readFileSync('package-lock.json','utf8'));
let licenses='Third-party licenses for the CodeMirror 6 editor bundle.\n\n';
let versions='CodeMirror 6 bundle for Jelly CMS (window.JellyCM)\nbuild date: '+date+'\nbundler: esbuild 0.28.2\n';
for(const [path,spec] of Object.entries(lock.packages)) {
 if(!path || path.includes('esbuild'))continue;
 const pkg=JSON.parse(fs.readFileSync(path+'/package.json','utf8'));
 const license=['LICENSE','LICENSE.txt','LICENSE.md'].find(name=>fs.existsSync(path+'/'+name));
 if(!license)throw Error('Missing license: '+pkg.name);
 licenses+='==== '+pkg.name+' '+pkg.version+' ====\n'+fs.readFileSync(path+'/'+license,'utf8')+'\n\n';
 const head=execFileSync('npm',['view',pkg.name+'@'+pkg.version,'gitHead'],{encoding:'utf8'}).trim();
 if(!/^[0-9a-f]{40}$/.test(head))throw Error('Missing source commit: '+pkg.name);
 const repo=typeof pkg.repository==='string'?pkg.repository:pkg.repository?.url;
 versions+=pkg.name+' '+pkg.version+' source '+repo+' commit '+head+'\n';
 versions+='tarball '+spec.resolved+' integrity '+spec.integrity+'\n';
}
fs.writeFileSync(out+'/LICENSE',licenses);
fs.writeFileSync(out+'/VERSION',versions);
JS
echo "wrote $out_dir/editor.js, LICENSE, VERSION"

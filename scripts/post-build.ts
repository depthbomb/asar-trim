import { join }                      from 'node:path';
import { stat, readFile, writeFile } from 'node:fs/promises';

const basedir = process.cwd();
const outfile = join(basedir, 'dist', 'cli.js');
const prepend = [
	'#!/usr/bin/env node',
	'/* eslint-disable */',
	'//prettier-ignore'
].join('\n');

stat(outfile).then(async () => {
	const contents = await readFile(outfile, 'utf8');

	await writeFile(outfile, `${prepend}\n${contents}`);
}).catch((err: unknown) => console.error('Could not find outfile', err));

import { join, dirname, basename } from 'node:path';
import { cp, stat, unlink, readdir } from 'node:fs/promises';
import type { Stats } from 'node:fs';

interface IWalkedFile {
	path: string;
	stats: Stats;
}

export async function fileExists(path: string): Promise<boolean> {
	try {
		await stat(path);

		return true;
	} catch {
		return false;
	}
}

export async function backupFile(path: string): Promise<string> {
	const filename   = basename(path);
	const filepath   = dirname(path);
	const backupName = `${filename}.bak`;
	const backupPath = join(filepath, backupName);

	if (await fileExists(backupPath)) await unlink(backupPath);

	await cp(path, backupPath);

	return backupPath;
}

export async function *walkDir(path: string): AsyncGenerator<IWalkedFile> {
	const files = await readdir(path, { withFileTypes: true });
	for (const file of files) {
		const filepath = join(path, file.name);
		const stats = await stat(filepath);
		if (stats.isDirectory()) {
			yield* walkDir(filepath);
		}

		yield { path: filepath, stats: stats };
	}
}

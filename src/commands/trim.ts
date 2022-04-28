import { createPackageWithOptions }        from 'asar';
import json5                               from 'json5';
import task                                from 'tasuku';
import { Option, Command }                 from 'clipanion';
import { join, resolve, basename }         from 'node:path';
import prettyBytes                         from 'pretty-bytes';
import { rm, unlink, readFile, writeFile } from 'node:fs/promises';
import { Worker }                          from 'node:worker_threads';

import { walkDir, fileExists, backupFile } from '../utils';

import type { CreateOptions }              from 'asar';
import type { BaseContext }                from 'clipanion';

export class TrimCommand extends Command<BaseContext> {
	public static override paths = [['trim'], ['t'], Command.Default];

	public asarPath = Option.String('-P,--path', {
		description: 'Path to your Electron app\'s "resources" directory where the "app.asar" file is located',
		arity: 1,
		required: true
	});

	public hintFilePath = Option.String('-H,--hint-file', {
		description: 'Path to your app\'s load order hint file, used to optimize file ordering',
		arity: 1,
		required: false,
	});

	public backup = Option.Boolean('-B,--backup', false, {
		description: 'Whether to backup the original app.asar file'
	});

	private _savedBytes: number = 0;

	private readonly _deletables = {
		files: [
			'.editorconfig',
			'.eslintignore',
			'.eslintrc',
			'.eslintrc.js',
			'.eslintrc.json',
			'.eslintrc.yml',
			'.gitmodules',
			'.jscs.json',
			'.jshintignore',
			'.lint',
			'.npmignore',
			'.nycrc.json',
			'.testignore',
			'.travis.yml',
			'.yarnrc.yml',
			'3rdpartylicenses.txt',
			'authors',
			'bower.json',
			'browsers.json',
			'changes',
			'ci.yml',
			'dependabot.yml',
			'funding.yml',
			'jasmine.json',
			'jsl.node.conf',
			'licence',
			'license',
			'license-mit.txt',
			'license.txt',
			'lint.yml',
			'makefile',
			'mocha.opts',
			'notice',
			'.nvmrc',
			'.nycrc',
			'rollup.config.js',
			'tsconfig.build.json',
			'tsconfig.eslint.json',
			'tsconfig.json',
			'tsdoc-metadata.json',
			'tslint.json',
			'webpack.build.config.js',
			'webpack.config.js',
			'yarn.lock',
		],
		extensions: [
			'.apache2',
			'.applescript',
			'.bak',
			'.cmd',
			'.coffee',
			'.flow',
			'.hbs',
			'.jsdoc',
			'.license.txt',
			'.map',
			'.markdown',
			'.md',
			'.mkd',
			'.nix',
			'.patch',
			'.scss',
			'.spec.js',
			'.swf',
			'.targ',
			'.template',
			'.tga',
			'.tgz',
			'.ts',
			'.webmanifest',
		],
		packageJSONProperties: [
			'author',
			'authors',
			'bin',
			'browser',
			'bundlesize',
			'description',
			'devDependencies',
			'engines',
			'funding',
			'gh-pages-deploy',
			'homepage',
			'husky',
			'imports',
			'jest',
			'jsdelivr',
			'license',
			'lint-staged',
			'maintainers',
			'np',
			'optionalDependencies',
			'packageManager',
			'peerDependencies',
			'peerDependenciesMeta',
			'prettier',
			'private',
			'publishConfig',
			'repository',
			'sideEffects',
			'testling',
			'tsd',
			'types',
			'typings',
			'unpkg',
		]
	};

	public async execute(): Promise<number> {
		this.asarPath = resolve(this.asarPath);

		const asarFile = resolve(this.asarPath, 'app.asar');
		const discoverTask = await task('Looking for app.asar...', async ({ setTitle, setError }) => {
			if (await fileExists(asarFile)) {
				setTitle('app.asar found');

				return true;
			}

			setError('app.asar not found');

			return false;
		});

		if (!discoverTask.result) return 1;

		if (this.backup) {
			const backupTask = await task('Backing up original app.asar...', async ({ setTitle, setError }) => {
				try {
					await backupFile(asarFile);

					setTitle('Backed up app.asar');
					return true;
				} catch (err: unknown) {
					
					setError(<Error>err);
					return false;
				}
			});

			if (!backupTask) return 1;
		}

		const appDir = join(this.asarPath, 'app');

		await task('Extracting app.asar...', async ({ setTitle }) => {
			return new Promise((resolve, reject) => {
				const workerPath = join(__dirname, 'extractWorker.js');
				const worker = new Worker(workerPath, { workerData: [asarFile, appDir] });
				
				worker.once('error', reject);
				worker.once('exit', (code: number) => {
					setTitle('Extracted app.asar');
					resolve(null);
				});
			});
		});

		await task('Processing files...', async ({ setTitle, setWarning, setError }) => {
			for await (const file of walkDir(appDir)) {
				const { path, stats } = file;
				const { size } = stats;
				const filename = basename(path);
	
				for (const deletableFile of this._deletables.files) {
					if (filename.toLowerCase() === deletableFile) {
						this._savedBytes = this._savedBytes + size;
						await unlink(path);
					}
				}
		
				for (const deletableExtension of this._deletables.extensions) {
					if (path.toLowerCase().endsWith(deletableExtension)) {
						if (await fileExists(path)) {
							this._savedBytes = this._savedBytes + size;
							await unlink(path);
						}
					}
				}
			
				if (filename === 'package.json') {
					const packageContents = await readFile(path, { encoding: 'utf8' });
					const packageJSON = json5.parse(packageContents);
					
					for (const packageProperty of this._deletables.packageJSONProperties) {
						if (packageProperty in packageJSON) {
							delete packageJSON[packageProperty];
						}
					}
			
					const newPackageJSON = JSON.stringify(packageJSON);
			
					await writeFile(path, newPackageJSON);
			
					const oldPackageSize = packageContents.length;
					const newPackageSize = newPackageJSON.length;
					const packageSizeDifference = oldPackageSize - newPackageSize;
			
					this._savedBytes = this._savedBytes + packageSizeDifference;
				} else if (filename.endsWith('.json')) {
					if (!await fileExists(path)) continue;
	
					const contents = await readFile(path, { encoding: 'utf8' });
	
					try {
						const json = json5.parse(contents);
						const newJSON = JSON.stringify(json);
			
						await writeFile(path, newJSON);
			
						const oldJSONSize = contents.length;
						const newJSONSize = newJSON.length;
						const jsonSizeDifference = oldJSONSize - newJSONSize;
				
						this._savedBytes = this._savedBytes + jsonSizeDifference;
					} catch (err: unknown) {
						// logger.warning('Unable to minify JSON file:', path, '- skipping');
					}
				}
			}

			setTitle('Finished processing files');
		});

		await task('Repacking', async ({ setTitle, setOutput }) => {
			const options: CreateOptions = {};
			if (this.hintFilePath) {
				this.hintFilePath = resolve(this.hintFilePath);
				if (!await fileExists(this.hintFilePath)) {
					setOutput(`Hint file could not be found at ${this.hintFilePath}, skipping`);
				} else {
					setOutput(`Using hint file ${this.hintFilePath}`);
					options.ordering = this.hintFilePath;
				}
			}

			await createPackageWithOptions(appDir, asarFile, options);
			await rm(appDir, { recursive: true });

			setTitle(`Reduced asar archive size by ${prettyBytes(this._savedBytes)}`);
		});

		return 0;
	};
};

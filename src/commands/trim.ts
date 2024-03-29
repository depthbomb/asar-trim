import { createPackageWithOptions }              from 'asar';
import json5                                     from 'json5';
import { convert }                               from 'convert';
import consola                                   from 'consola';
import { walkDir, fileExists }                   from '../utils';
import { Option, Command }                       from 'clipanion';
import { join, resolve, basename }               from 'node:path';
import { rm, stat, unlink, readFile, writeFile } from 'node:fs/promises';
import { Worker }                                from 'node:worker_threads';
import type { CreateOptions }                    from 'asar';
import type { BaseContext }                      from 'clipanion';

export class TrimCommand extends Command<BaseContext> {
	public static override paths = [Command.Default];

	public asarPath = Option.String();

	public hintFilePath = Option.String('-h,--hint-file', {
		description: 'Path to your app\'s load order hint file, used to optimize file ordering',
		arity: 1,
		required: false,
	});

	public backup = Option.Boolean('-b,--backup', false, {
		description: 'Whether to backup the original app.asar file'
	});

	public keepExtracted = Option.Boolean('-k,--keep-extracted', false, {
		description: 'Whether to keep the extracted app.asar contents after optimizing instead of deleting them'
	});

	private _savedBytes: number = 0;

	private readonly _deletables = {
		files: [
			'.airtap.yml',
			'.babelrc',
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
			'commitlint.config.js',
			'copyrightnotice.txt',
			'dependabot.yml',
			'funding.yml',
			'jasmine.json',
			'jest.config.js',
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
			'.__ivy_ngcc_bak',
			'.ai',
			'.apache2',
			'.applescript',
			'.babelrc',
			'.bak',
			'.c',
			'.cmd',
			'.coffee',
			'.cpp',
			'.cs',
			'.dds',
			'.dockerignore',
			'.flow',
			'.gyp',
			'.gypi',
			'.gz',
			'.h',
			'.hbs',
			'.hpp',
			'.huskyrc',
			'.ilk',
			'.info',
			'.inl',
			'.iobj',
			'.ipdb',
			'.jsdoc',
			'.jst',
			'.jsx',
			'.lib',
			'.license',
			'.license.txt',
			'.lintstagedrc',
			'.lock',
			'.log',
			'.map',
			'.markdown',
			'.md',
			'.mit',
			'.mkd',
			'.nix',
			'.patch',
			'.pdf',
			'.prettierignore',
			'.prettierrc',
			'.scss',
			'.spec.js',
			'.swf',
			'.targ',
			'.template',
			'.toml',
			'.tga',
			'.tgz',
			'.tlog',
			'.ts',
			'.tsbuildinfo',
			'.tsx',
			'.vcxproj',
			'.vdproj',
			'.vue',
			'.webmanifest',
			'.wrapped',
			'.x',
		],
		packageJSONProperties: [
			'author',
			'authors',
			'bin',
			'browser',
			'browserslist',
			'bundlesize',
			'commitlint',
			'config',
			'dependencies',
			'description',
			'devDependencies',
			'directories',
			'engine',
			'engines',
			'es2015',
			'es2020',
			'eslintIgnore',
			'esm2020',
			'exports',
			'fesm2015',
			'fesm2020',
			'files',
			'funding',
			'gh-pages-deploy',
			'gypfile',
			'homepage',
			'husky',
			'imports',
			'jest',
			'jsdelivr',
			'license',
			'licenses',
			'licenses',
			'lint-staged',
			'locales',
			'maintainers',
			'mocha',
			'modes',
			'ng-update',
			'np',
			'optionalDependencies',
			'optionalDevDependencies',
			'packageManager',
			'peerDependencies',
			'peerDependenciesMeta',
			'pre-commit',
			'prettier',
			'private',
			'publishConfig',
			'react-native',
			'readme',
			'readmeFilename',
			'repository',
			'schematics',
			'sideEffects',
			'standard',
			'testling',
			'tsd',
			'types',
			'typesVersions',
			'typescript',
			'typings',
			'typings',
			'unpkg',
			'verb',
		]
	};

	public async execute(): Promise<number | void> {
		this.asarPath = resolve(this.asarPath);

		const asarFile = resolve(this.asarPath, 'app.asar');
		if (!(await fileExists(asarFile))) {
			consola.fatal('app.asar not found at', this.asarPath);
			return 1;
		}

		if (this.backup) {
			const { backupFile } = await import('../utils');
			consola.info('Backing up app.asar');
			try {
				await backupFile(asarFile);
			} catch (err: unknown) {
				consola.fatal(err);
				return 1;
			}
		}

		consola.info('Extracting app.asar');

		const appDir = join(this.asarPath, 'app');
		const extractionResult = await new Promise<number | Error>((res, rej) => {
			const workerPath = join(__dirname, 'extractWorker.js');
			const worker = new Worker(workerPath, { workerData: [asarFile, appDir] });
			
			worker.once('error', (err: Error) => rej(err));
			worker.once('exit', (code: number) => res(code));
		});

		if (extractionResult !== 0) {
			consola.fatal(extractionResult);
			return 1;
		}

		consola.info('Extracted app.asar, processing files');

		// TODO rewrite pretty much all of this
		walk:
		for await (const file of walkDir(appDir)) {
			const { path, stats } = file;
			const { size } = stats;
			const filename = basename(path);

			for (const deletableFile of this._deletables.files) {
				if (filename.toLowerCase() !== deletableFile) continue;

				this._savedBytes = this._savedBytes + size;
				await unlink(path);

				continue walk;
			}
	
			for (const deletableExtension of this._deletables.extensions) {
				if (path.toLowerCase().endsWith(deletableExtension)) {
					if (!await fileExists(path) || stats.isDirectory()) continue;

					this._savedBytes = this._savedBytes + size;
					await unlink(path);

					continue walk;
				}
			}
		
			if (filename === 'package.json') {
				const bytesSaved = await this._minifyPackageJSON(path);
				this._savedBytes = this._savedBytes + bytesSaved;
			} else if (filename.endsWith('.json') || filename.endsWith('.json5')) {
				const contents = await readFile(path, { encoding: 'utf8' });

				try {
					const json = json5.parse(contents);
					const newJSON = JSON.stringify(json);
		
					await writeFile(path, newJSON);
		
					const oldJSONSize = contents.length;
					const newJSONSize = newJSON.length;
					const jsonSizeDifference = oldJSONSize - newJSONSize;
			
					this._savedBytes = this._savedBytes + jsonSizeDifference;
				} catch {}
			}
		}

		consola.info('Finished processing files, repacking');

		const options: CreateOptions = {};
		if (this.hintFilePath) {
			this.hintFilePath = resolve(this.hintFilePath);
			if (!await fileExists(this.hintFilePath)) {
				consola.warn(`Hint file could not be found at ${this.hintFilePath}, skipping`);
			} else {
				consola.info(`Using hint file ${this.hintFilePath}`);
				options.ordering = this.hintFilePath;
			}
		}

		await createPackageWithOptions(appDir, asarFile, options);

		if (!this.keepExtracted) {
			await rm(appDir, { recursive: true });
		}

		const savedMB = convert(this._savedBytes, 'bytes').to('MB');
		const roundedSavedMB = Math.round((savedMB + Number.EPSILON) * 100) / 100;

		consola.info(`Reduced asar archive size by ${roundedSavedMB}MB`);

		return 0;
	};

	private async _minifyPackageJSON(path: string): Promise<number> {
		const { size: originalSize } = await stat(path);
		const originalContents = await readFile(path, { encoding: 'utf8' });
		const originalData = json5.parse(originalContents);

		for (const packageProperty of this._deletables.packageJSONProperties) {
			if (packageProperty in originalData) {
				delete originalData[packageProperty];
			}
		}

		const newContents = JSON.stringify(originalData);
		await writeFile(path, newContents);

		const { size: newSize } = await stat(path);
		return originalSize - newSize;
	};
};

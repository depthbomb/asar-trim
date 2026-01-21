import json5 from 'json5';
import consola from 'consola';
import { convert } from 'convert';
import { Option, Command } from 'clipanion';
import { join, resolve, basename } from 'node:path';
import { walkDir, fileExists, backupFile } from '../utils';
import { extractAll, createPackageWithOptions } from '@electron/asar';
import { rm, stat, unlink, readFile, writeFile } from 'node:fs/promises';
import type { BaseContext } from 'clipanion';
import type { CreateOptions } from '@electron/asar';

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
			'bin',
			'browser',
			'browserslist',
			'bundlesize',
			'commitlint',
			'config',
			'description',
			'dependencies',
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
			'gh-pages-deploy',
			'gypfile',
			'husky',
			'imports',
			'jest',
			'jsdelivr',
			'lint-staged',
			'locales',
			'mocha',
			'modes',
			'name',
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
			'version',
		]
	};

	public async execute() {
		this.asarPath = resolve(this.asarPath);

		const asarFile = resolve(this.asarPath, 'app.asar');
		if (!(await fileExists(asarFile))) {
			consola.fatal('app.asar not found at', this.asarPath);
			return 1;
		}

		if (this.backup) {
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

		extractAll(asarFile, appDir);

		consola.info('Extracted app.asar, processing files');

		for await (const file of walkDir(appDir)) {
			const { path, stats } = file;
			const { size }        = stats;
			const filename        = basename(path).toLowerCase();

			if (this._deletables.files.includes(filename)) {
				this._savedBytes += size;
				await unlink(path);
				continue;
			}

			if (this._deletables.extensions.some(ext => path.toLowerCase().endsWith(ext))) {
				if (await fileExists(path) && !stats.isDirectory()) {
					this._savedBytes += size;
					await unlink(path);
				}
				continue;
			}

			if (filename === 'package.json') {
				const bytesSaved = await this._minifyPackageJSON(path);
				this._savedBytes += bytesSaved;
			} else if (filename.endsWith('.json') || filename.endsWith('.json5')) {
				try {
					const contents = await readFile(path, { encoding: 'utf8' });
					const json     = json5.parse(contents);
					const newJSON  = JSON.stringify(json);

					await writeFile(path, newJSON);

					this._savedBytes += contents.length - newJSON.length;
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

		const savedMB        = convert(this._savedBytes, 'bytes').to('MB');
		const roundedSavedMB = Math.round((savedMB + Number.EPSILON) * 100) / 100;

		consola.info(`Reduced asar archive size by ${roundedSavedMB}MB`);

		return 0;
	}

	private async _minifyPackageJSON(path: string) {
		const { size: originalSize } = await stat(path);
		const originalContents       = await readFile(path, { encoding: 'utf8' });
		const originalData           = json5.parse(originalContents);

		for (const packageProperty of this._deletables.packageJSONProperties) {
			if (packageProperty in originalData) {
				delete originalData[packageProperty];
			}
		}

		const newContents = JSON.stringify(originalData);

		await writeFile(path, newContents);

		const { size: newSize } = await stat(path);

		return originalSize - newSize;
	}
}

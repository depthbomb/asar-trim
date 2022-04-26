import json5                               from 'json5';
import prettyBytes                         from 'pretty-bytes';
import { Option, Command }                 from 'clipanion';
import { join, resolve, basename }         from 'node:path';
import { extractAll, createPackage }       from 'asar';
import { rm, unlink, readFile, writeFile } from 'node:fs/promises';

import { createLogger }                    from '../logger';
import { walkDir, fileExists, backupFile } from '../utils';

import type { BaseContext }                from 'clipanion';

export class TrimCommand extends Command<BaseContext> {
	public static override paths = [['trim'], ['t'], Command.Default];

	public asarPath = Option.String('-P,--path', {
		description: 'Path to your Electron app\'s "resources" directory where the "app.asar" file is located.',
		arity: 1,
		required: true
	});

	public backup = Option.Boolean('-B,--backup', true, {
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
		const logger = createLogger(this.context);
		this.asarPath = resolve(this.asarPath);

		const asarFile = resolve(this.asarPath, 'app.asar');
		if (!await fileExists(asarFile)) {
			this.context.stderr.write(`Asar file could not be found at ${this.asarPath}`);
			return 1;
		}

		if (this.backup) {
			await backupFile(asarFile);
			logger.info('Backed up app.asar');
			logger.separator();
		}

		const appDir = join(this.asarPath, 'app');

		logger.info('Extracting app.asar');

		extractAll(asarFile, appDir);

		logger.separator();
		logger.info('Processing files');

		for await (const file of walkDir(appDir)) {
			const { path, stats } = file;
			const { size } = stats;
			const filename = basename(path);

			for (const deletableFile of this._deletables.files) {
				if (filename.toLowerCase() === deletableFile) {
					this._savedBytes = this._savedBytes + size;
					await unlink(path);

					logger.success('Deleted file', path);
				}
			}
	
			for (const deletableExtension of this._deletables.extensions) {
				if (path.toLowerCase().endsWith(deletableExtension)) {
					if (await fileExists(path)) {
						this._savedBytes = this._savedBytes + size;
						await unlink(path);

						logger.success('Deleted file', path);
					}
				}
			}
		
			if (filename === 'package.json') {
				const packageContents = await readFile(path, { encoding: 'utf8' });
				const packageJSON = json5.parse(packageContents);
				
				for (const packageProperty of this._deletables.packageJSONProperties) {
					if (packageProperty in packageJSON) {
						delete packageJSON[packageProperty];

						logger.success('Deleted property', packageProperty, 'from', path);
					}
				}
		
				const newPackageJSON = JSON.stringify(packageJSON);
		
				await writeFile(path, newPackageJSON);
		
				const oldPackageSize = packageContents.length;
				const newPackageSize = newPackageJSON.length;
				const packageSizeDifference = oldPackageSize - newPackageSize;
		
				this._savedBytes = this._savedBytes + packageSizeDifference;

				logger.success('Wrote minified package.json file:', path);
			} else if (filename.endsWith('.json')) {
				if (!await fileExists(path)) continue;

				const contents = await readFile(path, { encoding: 'utf8' });
				const json = json5.parse(contents);
				const newJSON = JSON.stringify(json);
		
				await writeFile(path, newJSON);
	
				const oldJSONSize = contents.length;
				const newJSONSize = newJSON.length;
				const jsonSizeDifference = oldJSONSize - newJSONSize;
		
				this._savedBytes = this._savedBytes + jsonSizeDifference;

				logger.success('Wrote minified JSON file:', path);
			}
		}

		logger.separator();
		logger.info('Repacking app.asar');
		
		try {
			await createPackage(appDir, asarFile);

			logger.separator();
			logger.info('Cleaning up');
	
			await rm(appDir, { recursive: true });

			logger.separator();
			logger.success(`Reduced asar archive size by ${prettyBytes(this._savedBytes)}`);

			return 0;
		} catch (err: unknown) {
			logger.separator();

			this.context.stderr.write('Failed to re-pack asar:');
			this.context.stderr.write(err);

			return 1;
		}
	};
};

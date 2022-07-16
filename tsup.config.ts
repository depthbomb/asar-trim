import { defineConfig }   from 'tsup';
import { builtinModules } from 'node:module';
import { dependencies }   from './package.json';

export default defineConfig((options) => ({
	clean: true,
	entry: {
		cli: 'src/index.ts',
		extractWorker: 'src/extractWorker.ts'
	},
	format: 'cjs',
	dts: false,
	minify: true,
	noExternal: [
		...Object.keys(dependencies).filter(d => d !== 'asar'),
		...builtinModules.flatMap(p => [p, `node:${p}`]),
	],
	skipNodeModulesBundle: false,
	target: 'node16',
	tsconfig: './tsconfig.json',
	splitting: true,
	onSuccess: 'ts-node scripts/post-build'
}));

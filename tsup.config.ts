import { defineConfig } from 'tsup';

export default defineConfig((options) => ({
	clean: true,
	entry: {
		cli: 'src/index.ts',
		extractWorker: 'src/extractWorker.ts'
	},
	format: 'cjs',
	dts: false,
	minify: true,
	skipNodeModulesBundle: true,
	target: 'node16',
	tsconfig: './tsconfig.json',
	splitting: true,
	onSuccess: 'ts-node scripts/post-build'
}));

import { defineConfig } from 'tsup';

export default defineConfig(() => ({
	clean: true,
	entry: {
		cli: 'src/index.ts'
	},
	format: 'cjs',
	target: 'node22',
	dts: false,
	minify: true,
	skipNodeModulesBundle: false,
	sourcemap: true,
	splitting: false,
	keepNames: false,
	noExternal: [/.*/],
	tsconfig: './tsconfig.json',
	onSuccess: 'ts-node scripts/post-build'
}));

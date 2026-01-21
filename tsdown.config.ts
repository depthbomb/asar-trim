import { defineConfig } from 'tsdown';

export default defineConfig({
	clean: true,
	entry: {
		cli: 'src/index.ts'
	},
	format: 'cjs',
	target: 'node24',
	dts: false,
	minify: true,
	skipNodeModulesBundle: false,
	tsconfig: './tsconfig.json',
	onSuccess: 'ts-node scripts/post-build'
});

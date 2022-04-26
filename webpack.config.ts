import { resolve } from 'node:path';
import { BannerPlugin } from 'webpack';
import TerserPlugin from 'terser-webpack-plugin';
import nodeExternals from 'webpack-node-externals';

import type { Configuration } from 'webpack';

const production = process.env.NODE_ENV === 'production';

export default {
	mode: process.env.NODE_ENV ?? 'development',
	target: 'node',
	entry: './src/index.ts',
	output: {
		clean: true,
		path: resolve('./dist'),
		filename: 'cli.js',
	},
	optimization: {
		removeAvailableModules: true,
		sideEffects: false,
		minimize: production,
		minimizer: production ? [
			new TerserPlugin({
				extractComments: false
			})
		] : [],
	},
	resolve: {
		extensions: ['.ts', '.js', '.json'],
	},
	plugins: [],
	module: {
		rules: [
			{
				test: /\.[jt]s$/,
				loader: 'babel-loader',
			},
		]
	},
	externals: [
		nodeExternals({
			allowlist: ['chalk', 'clipanion', 'json5', 'pretty-bytes']
		})
	]
} as Configuration;

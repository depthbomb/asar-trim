import chalk                from 'chalk';
import { EOL }              from 'node:os';

import type { BaseContext } from 'clipanion';

const prefix         = '➤';
const infoColor      = chalk.hex('#06b6d4');
const warningColor   = chalk.hex('#ea580c');
const errorColor     = chalk.hex('#ef4444');
const successColor   = chalk.hex('#84cc16');
const separatorColor = chalk.hex('#475569');

// TODO get a better logger
function write(context: BaseContext, prefix: string, message: string, ...args: unknown[]) {
	context.stdout.write([
		prefix,
		message,
		...args
	].join(' ') + EOL);
};

export function createLogger(context: BaseContext) {
	return {
		info: (message: string, ...args: unknown[]) => write(context, infoColor(prefix), message, ...args),
		warning: (message: string, ...args: unknown[]) => write(context, warningColor(prefix), message, ...args),
		error: (message: string, ...args: unknown[]) => write(context, errorColor(prefix), message, ...args),
		success: (message: string, ...args: unknown[]) => write(context, successColor(prefix), message, ...args),
		separator: () => context.stdout.write(`${separatorColor(prefix)} ${'-'.repeat(process.stdout.columns - 2)}${EOL}`)
	};
};

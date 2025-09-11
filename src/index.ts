import { Cli } from 'clipanion';
import { TrimCommand } from './commands/trim';

const cli = new Cli({
	binaryLabel: 'Asar Trim',
	binaryName: 'asar-trim',
	binaryVersion: '1.2.0',
});

cli.register(TrimCommand);
cli.runExit(process.argv.slice(2));

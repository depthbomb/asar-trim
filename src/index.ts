import { Cli, Builtins } from 'clipanion';
import { TrimCommand }   from './commands/trim';

const cli = new Cli({
	binaryLabel: 'Asar Trim',
	binaryName: 'asar-trim',
	binaryVersion: '1.0.1',
});

cli.register(TrimCommand);
cli.register(Builtins.VersionCommand);
cli.register(Builtins.HelpCommand);

cli.runExit(process.argv.slice(2));

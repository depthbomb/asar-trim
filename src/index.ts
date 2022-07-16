import { Cli, Builtins } from 'clipanion';
import { VERSION }       from './constants';
import { TrimCommand }   from './commands/trim';

const cli = new Cli({
	binaryLabel: 'Asar Trim',
	binaryName: 'asar-trim',
	binaryVersion: VERSION,
});

cli.register(TrimCommand);
cli.register(Builtins.VersionCommand);
cli.register(Builtins.HelpCommand);

cli.runExit(process.argv.slice(2));

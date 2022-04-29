import { Cli, Builtins } from 'clipanion';

import pkg               from '../package.json';
import { TrimCommand }   from './commands/trim';
import { UpdateCommand } from './commands/update';

const cli = new Cli({
	binaryLabel: 'Asar Trimmer',
	binaryName: 'asar-trim',
	binaryVersion: pkg.version,
});

cli.register(TrimCommand);
cli.register(UpdateCommand);

cli.register(Builtins.VersionCommand);
cli.register(Builtins.HelpCommand);

cli.runExit(process.argv.slice(2));

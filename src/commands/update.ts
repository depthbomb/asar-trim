import { Command }          from 'clipanion';

import type { BaseContext } from 'clipanion';

export class TrimCommand extends Command<BaseContext> {
	public static override paths = [['check-updates'], ['u']];

	public async execute(): Promise<number> {
		return 0;
	};
};

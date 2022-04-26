import { Command }          from 'clipanion';

import { createLogger }     from '../logger';

import type { BaseContext } from 'clipanion';

export class TrimCommand extends Command<BaseContext> {
	public static override paths = [['check-updates'], ['u']];

	public async execute(): Promise<number> {
		const logger = createLogger(this.context);

		return 0;
	};
};

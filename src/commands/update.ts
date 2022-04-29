import { Command }          from 'clipanion';

import pkg                  from '../../package.json';

import type { BaseContext } from 'clipanion';

export class UpdateCommand extends Command<BaseContext> {
	public static override paths = [['check-updates'], ['update'], ['u']];

	private readonly _installedVersion: string;

	public constructor() {
		super();

		this._installedVersion = pkg.version;
	};

	public async execute(): Promise<number> {
		const { gt } = await import('semver');
		const { default: task } = await import('tasuku');
		const { request } = await import('@octokit/request');
		const code = await task('Checking for new versions...', async ({ setTitle, setError }) => {
			try {
				const { data: remoteTag } = await request('GET /repos/{owner}/{repo}/releases/latest', { owner: 'depthbomb', repo: 'asar-trim' });
				if (gt(remoteTag.tag_name, this._installedVersion)) {
					setTitle(`Version ${remoteTag.tag_name} of asar-trim is available. Use "npm i asar-trim@latest -g" to install.`);
				} else {
					setTitle('The latest version of asar-trim is installed.');
				}

				return 0;
			} catch (err: unknown) {
				setError(<Error>err);
				return 1;
			}
		});

		return code.result;
	};
};

<p align="center">
	<table>
		<tbody>
			<td align="center">
				<h1>asar-trim</h1>
				<p>An experimental CLI tool that trims unnecessary files from asar archives for Electron applications.</p>
				<p>
					<a href="https://www.npmjs.com/package/asar-trim"><img src="https://img.shields.io/npm/v/asar-trim?color=crimson&label=asar-trim&logo=npm&style=flat-square"></a>
					<a href="https://www.npmjs.com/package/asar-trim"><img src="https://img.shields.io/npm/dt/asar-trim?color=crimson&logo=npm&style=flat-square"></a>
					<a href="https://www.npmjs.com/package/asar-trim"><img src="https://img.shields.io/librariesio/release/npm/asar-trim?color=crimson&logo=npm&style=flat-square"></a>
				</p>
				<p>
					<a href="https://github.com/depthbomb/asar-trim/releases/latest"><img src="https://img.shields.io/github/release-date/depthbomb/asar-trim.svg?label=Released&logo=github&style=flat-square"></a>
					<a href="https://github.com/depthbomb/asar-trim/releases/latest"><img src="https://img.shields.io/github/release/depthbomb/asar-trim.svg?label=Stable&logo=github&style=flat-square"></a>
					<a href="https://github.com/depthbomb/asar-trim"><img src="https://img.shields.io/github/repo-size/depthbomb/asar-trim.svg?label=Repo%20Size&logo=github&style=flat-square"></a>
					<a href="https://github.com/depthbomb/asar-trim/releases/latest"><img src="https://img.shields.io/github/downloads/depthbomb/asar-trim/latest/total.svg?label=Downloads&logo=github&style=flat-square"></a>
				</p>
				<p>
					<a href="https://ko-fi.com/O4O1DV77" target="_blank"><img height="36" src="https://cdn.ko-fi.com/cdn/kofi1.png?v=3" alt="Buy Me a Coffee at ko-fi.com" /></a>
				</p>
				<img width="2000" height="0">
			</td>
		</tbody>
	</table>
</p>

# Installation

```
npm i asar-trim -g
```

# Usage

```
$ asar-trim -P <path to resources containing app.asar>
```

# What is it?

Many popular Electron applications today sure like to include their dev dependencies, raw frontend assets, and even source files in their final package which inflates the already-large size to greater lengths.

This tool attempts to delete these files and repack the asar file to produce a much slimmer archive.

# What does it do?

There are a few things `asar-trim` will do when it does its optimization:

- Deletes various files that have no use in the application
  - You can see what is deleted [here](https://github.com/depthbomb/asar-trim/blob/master/src/commands/trim.ts#L35)
- Minifies .json files
- Minifies and removes unneeded properties from package.json files

# Options

```
# Creates a backup of the original app.asar file
-B, --backup

# Path to your app's generated load order hint file, see https://github.com/atom/atom/issues/10163 and https://www.electronjs.org/docs/latest/api/environment-variables#electron_log_asar_reads
-H, --hint-file
```

# Planned Features

- Trimming app.asar.unpacked directory
- Update checking command
- Improved backing up
- Additional options to customize what files will be deleted

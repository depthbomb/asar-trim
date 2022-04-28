import { extractAll } from 'asar';
import { workerData } from 'node:worker_threads';

const [asarFile, appDir] = workerData;

extractAll(asarFile, appDir);

import { defineConfig } from 'vite';
import { ocmanPlugin } from '@ocman/plugin-vite';

export default defineConfig({ plugins: [ocmanPlugin()] });

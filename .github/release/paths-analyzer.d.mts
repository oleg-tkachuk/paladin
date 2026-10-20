// Types for paths-analyzer.mjs, which the TypeScript in .github/ci imports;
// semantic-release loads the plugin itself, so it stays JavaScript.

export interface PathFilter {
  include?: string[];
  exclude?: string[];
}

export interface Documentation {
  prefixes?: string[];
  suffixes?: string[];
}

export function belongs(files: readonly string[], filter?: PathFilter): boolean;
export function within(files: readonly string[], prefixes?: readonly string[]): boolean;
export function isDocumentation(file: string, documentation?: Documentation): boolean;
export function deciding(files: readonly string[], documentation: Documentation | undefined): string[];
export function filesOf(hash: string, cwd?: string): string[];

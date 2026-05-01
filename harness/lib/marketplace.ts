/**
 * VS Code Marketplace helpers. Pure functions; no I/O.
 */

export interface PublisherName {
  publisher: string;
  name: string;
}

/**
 * Split a Marketplace extension id into publisher and name.
 * The publisher segment is everything before the FIRST dot;
 * everything after is the name (which may itself contain dots).
 */
export function splitExtensionId(id: string): PublisherName {
  const dot = id.indexOf(".");
  if (dot <= 0 || dot === id.length - 1) {
    throw new Error(`Invalid extension id "${id}": expected publisher.name`);
  }
  return {
    publisher: id.slice(0, dot),
    name: id.slice(dot + 1),
  };
}

/**
 * Build the Marketplace download URL for a specific version of an extension.
 * Returns a vspackage URL that responds with the .vsix bytes (gzip-encoded).
 */
export function buildVsixUrl(id: string, version: string): string {
  const { publisher, name } = splitExtensionId(id);
  return `https://marketplace.visualstudio.com/_apis/public/gallery/publishers/${publisher}/vsextensions/${name}/${version}/vspackage`;
}

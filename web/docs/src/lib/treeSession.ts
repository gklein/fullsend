/** Keys match `DocTreeNav` (`fullsend-docs-tree:${dirPath}`). */
const TREE_KEY_PREFIX = "fullsend-docs-tree:";

/** Mark `dirPath` and every ancestor as expanded (`"1"`) so the tree can open once without forcing expansion on every render. */
export function persistExpandedPathInSession(dirPath: string): void {
  const segments = dirPath.split("/").filter(Boolean);
  let acc = "";
  for (const seg of segments) {
    acc = acc ? `${acc}/${seg}` : seg;
    try {
      sessionStorage.setItem(`${TREE_KEY_PREFIX}${acc}`, "1");
    } catch {
      /* quota / private mode */
    }
  }
}

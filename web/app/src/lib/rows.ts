import type { MouseEvent } from "react";
import { useNavigate } from "react-router-dom";

/**
 * Makes a whole table row behave like its own link.
 *
 * The first cell keeps a real `<a>`, which is what makes the row reachable by
 * keyboard, openable in a new tab, and copyable — a `<tr>` cannot be any of
 * those. This handler only covers the pointer path for the rest of the row.
 *
 * A click that lands on a control inside the row belongs to that control: the
 * queue has checkboxes, retry/skip buttons and a priority menu, and a row that
 * steals those clicks would be worse than a row that does nothing.
 */
export function useRowClick() {
  const navigate = useNavigate();
  return (to: string) => (e: MouseEvent<HTMLElement>) => {
    const target = e.target as HTMLElement | null;
    if (target?.closest("a,button,input,label,select,textarea")) return;
    // A modified click is a request for a new tab or window, which only a real
    // anchor can honour — so it is left to the anchor.
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
    navigate(to);
  };
}

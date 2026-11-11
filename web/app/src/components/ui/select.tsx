import { Select as SelectPrimitive } from "radix-ui";
import { CaretDown, Check } from "@phosphor-icons/react";
import type { ReactNode } from "react";

/**
 * The one dropdown: Radix Select (the shadcn primitive) wearing canon chrome — 32px, square,
 * `border-strong`, flat surface. Never render a native `<select>`: the OS menu ignores the theme
 * and looks nothing like the rest of the app.
 */

const trigger =
  "inline-flex h-8 items-center justify-between gap-2 border border-border-strong bg-[var(--surface)] px-2.5 text-sm text-[var(--foreground)] transition-colors duration-150 hover:bg-[var(--surface-2)] focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50";

const content =
  "z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden border border-border-strong bg-[var(--surface)] text-[var(--foreground)]";

const item =
  "relative flex h-8 cursor-default select-none items-center gap-2 px-2.5 pr-7 text-sm outline-none data-[highlighted]:bg-[var(--surface-2)] data-[disabled]:opacity-50";

export const Select = SelectPrimitive.Root;
export const SelectValue = SelectPrimitive.Value;
export const SelectGroup = SelectPrimitive.Group;

export function SelectTrigger({
  children,
  className,
  ...props
}: SelectPrimitive.SelectTriggerProps) {
  return (
    <SelectPrimitive.Trigger className={[trigger, className ?? ""].join(" ").trim()} {...props}>
      {children}
      <SelectPrimitive.Icon asChild>
        <CaretDown size={14} aria-hidden="true" className="text-[var(--text-3)]" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  );
}

export function SelectContent({
  children,
  className,
  ...props
}: SelectPrimitive.SelectContentProps) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        position="popper"
        sideOffset={4}
        className={[content, className ?? ""].join(" ").trim()}
        {...props}
      >
        <SelectPrimitive.Viewport className="p-0">{children}</SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  );
}

export function SelectItem({
  children,
  className,
  ...props
}: SelectPrimitive.SelectItemProps & { children: ReactNode }) {
  return (
    <SelectPrimitive.Item className={[item, className ?? ""].join(" ").trim()} {...props}>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator className="absolute right-2 inline-flex">
        <Check size={12} weight="bold" aria-hidden="true" />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  );
}

import { IconSearch } from "./icons.tsx";

export default function Search(props: { defaultValue: string | null }) {
  return (
    <form action="/" method="get" class="w-full">
      <label class="sr-only" htmlFor="query">Søk etter NRK-podkast</label>
      <div class="relative">
        <span class="absolute left-5 top-1/2 -translate-y-1/2 text-ink-soft dark:text-ink-soft-dark pointer-events-none">
          <IconSearch size={22} />
        </span>
        <input
          type="search"
          id="query"
          name="query"
          placeholder="Søk etter en NRK-podkast …"
          defaultValue={props.defaultValue ?? ""}
          autocomplete="off"
          class="w-full rounded-full border-2 border-line dark:border-line-dark bg-paper-raised dark:bg-paper-raised-dark pl-14 pr-28 py-4 text-lg placeholder:text-ink-soft/70 dark:placeholder:text-ink-soft-dark/70 focus:outline-none focus:border-accent dark:focus:border-accent-dark shadow-sm"
        />
        <button
          type="submit"
          class="absolute right-2 top-1/2 -translate-y-1/2 rounded-full bg-accent hover:bg-accent-strong dark:bg-accent-dark dark:hover:bg-accent text-white dark:text-paper-dark font-semibold px-6 py-2.5 transition-colors cursor-pointer"
        >
          Søk
        </button>
      </div>
    </form>
  );
}

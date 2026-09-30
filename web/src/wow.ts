import type { Flavour, WowCharacter } from "./api";

// Class colours as used in game, by playable class ID.
const classColors: Record<number, string> = {
  1: "#C69B6D", // Warrior
  2: "#F48CBA", // Paladin
  3: "#AAD372", // Hunter
  4: "#FFF468", // Rogue
  5: "#FFFFFF", // Priest
  6: "#C41E3A", // Death Knight
  7: "#0070DD", // Shaman
  8: "#3FC7EB", // Mage
  9: "#8788EE", // Warlock
  10: "#00FF98", // Monk
  11: "#FF7C0A", // Druid
  12: "#A330C9", // Demon Hunter
  13: "#33937F", // Evoker
};

export const classColor = (classId: number) => classColors[classId] ?? "#888888";

/** Every flavour, in display order. */
export const allFlavours: Flavour[] = ["retail", "classic", "classic_era"];

export const flavourLabel = (f: Flavour) => ({ retail: "Retail", classic: "Classic", classic_era: "Classic Era" })[f];

/** Stable key for a character (IDs are only unique within a flavour and region). */
export const characterKey = (c: Pick<WowCharacter, "flavour" | "region" | "character_id">) =>
  `${c.flavour}-${c.region}-${c.character_id}`;

/** "Name - Realm (US)" */
export const characterLabel = (c: WowCharacter) => `${c.name} - ${c.realm} (${c.region.toUpperCase()})`;

/** "Level 80 Blood Elf Paladin" */
export const characterDetail = (c: WowCharacter) => `Level ${c.level} ${c.race} ${c.class}`;

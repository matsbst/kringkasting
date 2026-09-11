import { fakerNB_NO as faker } from "@faker-js/faker";
import { Episode, Series } from "./storage.ts";

function generateSeries(overrides: Partial<Series> = {}): Series {
  return {
    id: faker.word.words(1),
    title: faker.word.words(3),
    subtitle: faker.word.words(3),
    link: faker.internet.url(),
    imageUrl: faker.image.url(),
    lastFetchedAt: faker.date.recent(),
    episodes: new Array(faker.number.int({ min: 1, max: 100 }))
      .fill(null).map(() => generateEpisode()),
    ...overrides,
  };
}

function generateEpisode(overrides: Partial<Episode> = {}): Episode {
  return {
    title: faker.word.words(3),
    subtitle: faker.word.words(3),
    url: faker.internet.url(),
    shareLink: faker.internet.url(),
    id: faker.string.uuid(),
    date: faker.date.recent(),
    durationInSeconds: faker.number.int({
      min: 0,
      max: 3600,
    }),
    bytes: null,
    ...overrides,
  };
}

export const testUtils = {
  generateSeries,
  generateEpisode,
};

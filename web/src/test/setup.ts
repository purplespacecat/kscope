// Vitest setup, loaded before every test file (vite.config.ts `test.setupFiles`).
//
// The jest-dom import does two jobs: registers the DOM matchers
// (toBeInTheDocument, ...) on vitest's expect at runtime, and — because this
// file is part of the tsconfig program — its module augmentation makes those
// matchers type-check in every test file.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Unmount React trees between tests. RTL does this automatically only when
// its afterEach hook is injected via globals, which we don't enable.
afterEach(cleanup);

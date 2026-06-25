import { test } from "node:test";

import { expect } from "expect";

import {
  type Coordinates,
  InMemoryPackageSource,
  resolveTransitive,
  toCoordinates,
} from "../packages/index.ts";
import { buildForest, formatTree, type TreeSection } from "./tree.ts";

const coord = (spec: string): Coordinates => {
  const [g, a, v] = spec.split(":");
  return toCoordinates(g!, a!, v!);
};

function source(): InMemoryPackageSource {
  const pkg = (spec: string, deps: string[] = []) => ({
    coordinates: coord(spec),
    dependencies: deps.map(coord),
  });
  return new InMemoryPackageSource("registry", [
    pkg("org.x:app:1.0", ["org.x:lib:2.0", "org.y:util:3.0"]),
    pkg("org.x:lib:2.0", ["org.y:util:3.0"]),
    pkg("org.y:util:3.0"),
  ]);
}

test("buildForest nests transitive dependencies under their requester", async () => {
  const resolution = await resolveTransitive([coord("org.x:app:1.0")], [source()]);
  // org.y:util:3.0 is reached first via org.x:app (nearest wins), so it nests
  // there and not again under org.x:lib.
  expect(buildForest(resolution)).toEqual([
    {
      coordinate: "org.x:app:1.0",
      dependencies: [
        { coordinate: "org.x:lib:2.0", dependencies: [] },
        { coordinate: "org.y:util:3.0", dependencies: [] },
      ],
    },
  ]);
});

test("buildForest marks declared roots that no source can resolve", async () => {
  const resolution = await resolveTransitive([coord("org.z:missing:9.9")], [source()]);
  expect(buildForest(resolution)).toEqual([
    { coordinate: "org.z:missing:9.9", dependencies: [], unresolved: true },
  ]);
});

test("formatTree renders one indented section per non-empty configuration", () => {
  const sections: TreeSection[] = [
    {
      configuration: "api",
      tree: [
        {
          coordinate: "org.x:app:1.0",
          dependencies: [
            { coordinate: "org.x:lib:2.0", dependencies: [] },
            { coordinate: "org.y:util:3.0", dependencies: [], unresolved: true },
          ],
        },
      ],
    },
    { configuration: "implementation", tree: [] }, // empty: skipped
  ];
  expect(formatTree(sections)).toBe(
    [
      "api",
      "└── org.x:app:1.0",
      "    ├── org.x:lib:2.0",
      "    └── org.y:util:3.0 (unresolved)",
      "",
    ].join("\n"),
  );
});

test("formatTree reports when nothing is declared", () => {
  expect(formatTree([{ configuration: "api", tree: [] }])).toBe("no dependencies declared\n");
});

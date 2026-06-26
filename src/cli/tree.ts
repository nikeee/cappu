// `cappu tree`: resolve each dependency configuration's transitive graph and
// print it as an indented tree (npm-ls / cargo-tree style), one section per
// configuration (api, implementation, annotationProcessor, testImplementation).
// --json emits the same forest machine-readable. Builds purely on the
// `requestedBy` edges that `resolveTransitive` records - no new graph code.

import { type CappuConfig, DEPENDENCY_CONFIGURATIONS } from "../config.ts";
import { configuredSources, configurationRoots, type UpdateConfiguration } from "../install.ts";
import {
  coordinatesToString,
  packageKey,
  type PackageSource,
  type Resolution,
  type ResolvedPackage,
  resolveTransitive,
} from "../packages/index.ts";
import { colorEnabled } from "./color.ts";
import { painter } from "./style.ts";

export interface TreeNode {
  coordinate: string;
  dependencies: TreeNode[];
  /** A declared dependency no source could provide. */
  unresolved?: boolean;
}

export interface TreeSection {
  configuration: UpdateConfiguration;
  tree: TreeNode[];
}

// Turn one configuration's resolution into a forest by following each package's
// single `requestedBy` parent. Nearest-wins dedup means every package appears
// exactly once, so no shared-subtree markers are needed; the cycle guard is
// belt-and-suspenders against a pathological requestedBy loop.
export function buildForest(resolution: Resolution): TreeNode[] {
  const childrenByParent = new Map<string, ResolvedPackage[]>();
  const roots: ResolvedPackage[] = [];
  for (const p of resolution.packages) {
    if (p.requestedBy === undefined) {
      roots.push(p);
      continue;
    }
    const key = packageKey(p.requestedBy);
    let list = childrenByParent.get(key);
    if (!list) {
      list = [];
      childrenByParent.set(key, list);
    }
    list.push(p);
  }

  const seen = new Set<string>();
  const toNode = (p: ResolvedPackage): TreeNode => {
    const key = packageKey(p.coordinates);
    const node: TreeNode = { coordinate: coordinatesToString(p.coordinates), dependencies: [] };
    if (seen.has(key)) return node;
    seen.add(key);
    node.dependencies = (childrenByParent.get(key) ?? []).map(toNode);
    return node;
  };

  const forest = roots.map(toNode);
  // Surface declared roots that nothing could resolve - otherwise they vanish.
  for (const m of resolution.missing) {
    if (m.requestedBy === undefined) {
      forest.push({
        coordinate: coordinatesToString(m.coordinates),
        dependencies: [],
        unresolved: true,
      });
    }
  }
  return forest;
}

type Paint = ReturnType<typeof painter>;
const plain: Paint = (_format, text) => text;

function renderNodes(nodes: readonly TreeNode[], prefix: string, paint: Paint): string[] {
  const lines: string[] = [];
  nodes.forEach((node, i) => {
    const last = i === nodes.length - 1;
    const label = node.unresolved
      ? `${node.coordinate} ${paint("yellow", "(unresolved)")}`
      : node.coordinate;
    lines.push(`${prefix}${last ? "└── " : "├── "}${label}`);
    lines.push(...renderNodes(node.dependencies, `${prefix}${last ? "    " : "│   "}`, paint));
  });
  return lines;
}

/** Render the per-configuration forests as indented trees (pure, testable). */
export function formatTree(sections: readonly TreeSection[], paint: Paint = plain): string {
  const nonEmpty = sections.filter(s => s.tree.length > 0);
  if (nonEmpty.length === 0) return "no dependencies declared\n";
  const blocks = nonEmpty.map(s =>
    [paint(["bold", "cyan"], s.configuration), ...renderNodes(s.tree, "", paint)].join("\n"),
  );
  return `${blocks.join("\n")}\n`;
}

export async function runTree(
  config: CappuConfig,
  options: { json?: boolean } = {},
  sources: readonly PackageSource[] = configuredSources(config),
): Promise<never> {
  let resolving = 0;
  const onResolve = () => {
    if (colorEnabled(process.stderr.isTTY)) {
      process.stderr.write(`\r\x1b[2Kresolving dependency graph (${++resolving})...`);
    }
  };

  const sections: TreeSection[] = [];
  for (const configuration of DEPENDENCY_CONFIGURATIONS) {
    const resolution = await resolveTransitive(
      configurationRoots(config, configuration),
      sources,
      onResolve,
    );
    sections.push({ configuration, tree: buildForest(resolution) });
  }
  if (resolving > 0) process.stderr.write("\r\x1b[2K");

  if (options.json) {
    const json = sections.filter(s => s.tree.length > 0);
    process.stdout.write(`${JSON.stringify(json, null, 2)}\n`);
    process.exit(0);
  }

  process.stdout.write(formatTree(sections, painter(process.stdout)));
  process.exit(0);
}

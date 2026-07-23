import type {
  ContractCatalogOperation,
  ContractCatalogRelationship,
  ContractCatalogSource,
} from '../api'

export type DependencyEdgeKind = 'implementation' | 'caller' | 'unresolved_candidate'

export interface DependencyEdge {
  id: string
  kind: DependencyEdgeKind
  label: string
  classification: string
  reason?: string
  lineage?: string
  tier: string
  codeRole?: string
  source: ContractCatalogSource
}

export interface PositionedDependencyEdge extends DependencyEdge {
  side: 'left' | 'right'
  x: number
  y: number
}

export interface DependencyLayout {
  width: number
  height: number
  centerX: number
  centerY: number
  edges: PositionedDependencyEdge[]
}

const kindOrder: Record<DependencyEdgeKind, number> = {
  implementation: 0,
  caller: 1,
  unresolved_candidate: 2,
}

export function buildDependencyEdges(
  operation: ContractCatalogOperation,
): DependencyEdge[] {
  const relationships: ContractCatalogRelationship[] = [
    ...operation.implementations,
    ...operation.callers,
    ...operation.unresolved_candidates,
  ]
  const edges = relationships.flatMap((relationship) =>
    relationship.claim.sources.map((source) => ({
      id: dependencyEdgeID(relationship, source),
      kind: relationship.kind,
      label: dependencyEdgeLabel(relationship.kind, source),
      classification: relationship.classification,
      reason: relationship.reason,
      lineage: relationship.claim.lineage,
      tier: relationship.claim.tier,
      codeRole: relationship.claim.code_role,
      source,
    })))
  edges.sort((left, right) =>
    kindOrder[left.kind] - kindOrder[right.kind] ||
    left.id.localeCompare(right.id))
  return edges
}

export function layoutDependencyEdges(
  operation: ContractCatalogOperation,
): DependencyLayout {
  const edges = buildDependencyEdges(operation)
  const providers = edges.filter((edge) => edge.kind === 'implementation')
  const consumers = edges.filter((edge) => edge.kind !== 'implementation')
  const rowCount = Math.max(providers.length, consumers.length, 1)
  const height = Math.max(250, 80 + rowCount * 64)
  const centerY = Math.round(height / 2)
  return {
    width: 960,
    height,
    centerX: 480,
    centerY,
    edges: [
      ...providers.map((edge, index) => ({
        ...edge, side: 'left' as const, x: 18, y: 48 + index * 64,
      })),
      ...consumers.map((edge, index) => ({
        ...edge, side: 'right' as const, x: 702, y: 48 + index * 64,
      })),
    ],
  }
}

export function relationshipName(kind: DependencyEdgeKind): string {
  if (kind === 'implementation') return 'Registration provider'
  if (kind === 'caller') return 'Caller evidence'
  return 'Unresolved candidate'
}

function dependencyEdgeID(
  relationship: ContractCatalogRelationship,
  source: ContractCatalogSource,
): string {
  return [
    relationship.kind,
    relationship.claim.assertion_id,
    source.atom_id,
    source.repository,
    source.commit,
    source.path,
    source.start_byte,
    source.end_byte,
  ].join(':')
}

function dependencyEdgeLabel(
  kind: DependencyEdgeKind,
  source: ContractCatalogSource,
): string {
  return `${relationshipName(kind)} · ${source.repository}/${source.path}:${source.start_line}`
}

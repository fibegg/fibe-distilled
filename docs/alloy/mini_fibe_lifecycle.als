module fibe_distilled_lifecycle

abstract sig Status {}
one sig Pending, InProgress, Running, ErrorStatus, HasChanges,
        Stopping, Stopped, Destroying, Deleted extends Status {}

abstract sig RuntimeObservation {}
one sig RuntimeUnknown, RuntimeHealthy, RuntimeStopped, RuntimeMissing,
        RuntimeFailed extends RuntimeObservation {}

abstract sig WorkflowKind {}
one sig BuildWorkflow, SourceOnlyWorkflow, PullWorkflow extends WorkflowKind {}

abstract sig BuildStatus {}
one sig BuildPending, BuildBuilding, BuildSuccess, BuildFailed extends BuildStatus {}

abstract sig RouteMode {}
one sig NoRoute, PublicRoute, InternalRoute extends RouteMode {}

abstract sig Registry {}
one sig GHCR, DockerHub, OtherRegistry extends Registry {}

abstract sig AuthRequirement {}
one sig AuthRequired, AuthOptional extends AuthRequirement {}

abstract sig AuthState {}
one sig AuthPresent, AuthAbsent extends AuthState {}

sig Prop {}
sig BranchName {}
sig ServiceName {}
sig DockerfilePath {}
sig BuildTarget {}
sig BuildArgsDigest {}
sig SourceRevision {}

sig Playspec {}

sig Playground {
  spec: one Playspec
}

sig PropBranch {
  branchProp: one Prop,
  name: one BranchName
}

sig BuildIdentity {
  dockerfile: one DockerfilePath,
  target: lone BuildTarget,
  buildArgs: one BuildArgsDigest
}

sig Service {
  name: one ServiceName,
  sourceProp: lone Prop,
  sourceBranch: lone BranchName,
  workflow: one WorkflowKind,
  buildIdentity: lone BuildIdentity,
  route: one RouteMode,
  registry: one Registry,
  authRequired: one AuthRequirement,
  auth: one AuthState
}

sig BuildRecord {
  playground: one Playground,
  prop: lone Prop,
  branch: one BranchName,
  service: one ServiceName,
  commit: one SourceRevision,
  buildIdentity: one BuildIdentity
}

sig World {
  status: Playground -> one Status,
  errorDetails: set Playground,
  runtime: Playground -> one RuntimeObservation,
  filesystemReady: set Playground,
  safeRuntimeProject: set Playground,
  cleanedRuntime: set Playground,
  dirtySource: set Playground,
  expired: set Playground,
  routeReady: set Playground,
  traefikReady: set Playground,
  rootDomainPresent: set Playground,
  marqueeBacked: set Playground,
  services: Playground -> set Service,
  visibleBuilds: Playground -> set BuildRecord,
  buildStatus: BuildRecord -> one BuildStatus,
  staleBuild: set BuildRecord,
  remoteCheckout: BuildRecord -> lone SourceRevision,
  imageSourceCommit: BuildRecord -> lone SourceRevision,
  imageMetadataCommit: BuildRecord -> lone SourceRevision,
  protectedLocalWork: set Playground,
  needsRecreation: set Playground,
  sourceSynced: set Playground
}

fun active[w: World]: set Playground {
  { p: Playground | w.status[p] in Pending + InProgress + Running + HasChanges + Stopping + Destroying }
}

pred buildServiceMatchesRecord[p: Playground, s: Service, b: BuildRecord] {
  b.playground = p
  s.workflow = BuildWorkflow
  s.sourceBranch = b.branch
  s.name = b.service
  s.buildIdentity = b.buildIdentity
  some b.prop implies s.sourceProp = b.prop
  no b.prop implies no s.sourceProp
}

fact ScopedRuntimeInvariants {
  all w: World, p: Playground |
    w.status[p] = ErrorStatus implies p in w.errorDetails

  all w: World, p: Playground |
    w.status[p] = Running implies {
      w.runtime[p] = RuntimeHealthy
      p in w.filesystemReady
      p not in w.errorDetails
    }

  all w: World, p: Playground |
    p in w.routeReady implies {
      p in w.traefikReady
      p in w.rootDomainPresent
      some s: w.services[p] | s.route in PublicRoute + InternalRoute
    }

  all w: World, p: Playground |
    p in w.cleanedRuntime implies p in w.safeRuntimeProject

  all disj left, right: PropBranch |
    not (left.branchProp = right.branchProp and left.name = right.name)

  all disj left, right: BuildIdentity |
    left.dockerfile != right.dockerfile
    or left.target != right.target
    or left.buildArgs != right.buildArgs

  all s: Service |
    (s.workflow = BuildWorkflow) iff some s.buildIdentity

  all s: Service |
    s.authRequired = AuthRequired implies s.auth = AuthPresent

  all w: World, p: Playground, b: BuildRecord |
    b in w.visibleBuilds[p] implies some s: w.services[p] | buildServiceMatchesRecord[p, s, b]

  all w: World, b: BuildRecord |
    w.buildStatus[b] = BuildSuccess implies {
      w.remoteCheckout[b] = b.commit
      w.imageSourceCommit[b] = b.commit
      w.imageMetadataCommit[b] = b.commit
    }

  all w: World, b: BuildRecord |
    b in w.staleBuild implies w.buildStatus[b] = BuildBuilding

  all w: World, p: Playground |
    p in w.needsRecreation implies {
      p not in w.protectedLocalWork
    }

  all w: World, p: Playground |
    p in w.sourceSynced implies p not in w.protectedLocalWork
}

pred expireClean[w, wNext: World, p: Playground] {
  p in w.expired
  p not in w.dirtySource
  p in w.safeRuntimeProject
  w.status[p] in Running + ErrorStatus
  wNext.status[p] = Deleted
  p in wNext.cleanedRuntime
}

pred expireDirty[w, wNext: World, p: Playground] {
  p in w.expired
  p in w.dirtySource
  w.status[p] in Running + ErrorStatus
  wNext.status[p] = HasChanges
  p not in wNext.cleanedRuntime
}

pred failedDeploy[w: World, p: Playground] {
  w.status[p] = ErrorStatus
  p in w.errorDetails
}

run RunningStandardPlayground {
  some w: World, p: Playground |
    w.status[p] = Running
} for 4

run FailedDeployIsInspectable {
  some w: World, p: Playground |
    failedDeploy[w, p]
} for 4

run CleanExpirationDestroysSafeRuntimeProject {
  some w, wNext: World, p: Playground |
    expireClean[w, wNext, p]
} for 4

run RoutedStandardPlaygroundNeedsTraefikAndDomain {
  some w: World, p: Playground, s: Service |
    w.status[p] = Running and
    s in w.services[p] and
    s.route = PublicRoute and
    p in w.routeReady
} for 5

run BuildRecordSuccessCarriesExactSourceIdentity {
  some w: World, b: BuildRecord |
    w.buildStatus[b] = BuildSuccess
} for 5

check ErrorPlaygroundHasDiagnostics {
  no w: World, p: Playground |
    w.status[p] = ErrorStatus and p not in w.errorDetails
} for 5

check DestroyCleanupRequiresSafeRuntimeProject {
  no w: World, p: Playground |
    p in w.cleanedRuntime and p not in w.safeRuntimeProject
} for 5

check DirtyExpirationDoesNotDeleteRuntime {
  all w, wNext: World, p: Playground |
    expireDirty[w, wNext, p] implies wNext.status[p] = HasChanges and p not in wNext.cleanedRuntime
} for 5

check RunningStandardPlaygroundRequiresRuntimeEvidence {
  no w: World, p: Playground |
    w.status[p] = Running and
    (w.runtime[p] != RuntimeHealthy or p not in w.filesystemReady or p in w.errorDetails)
} for 5

check RoutedPlaygroundRequiresTraefikAndRootDomain {
  no w: World, p: Playground |
    p in w.routeReady and (p not in w.traefikReady or p not in w.rootDomainPresent)
} for 5

check PropBranchesAreUniquePerPropName {
  no disj left, right: PropBranch |
    left.branchProp = right.branchProp and left.name = right.name
} for 6

check BuildIdentityIncludesDockerShape {
  all left, right: BuildIdentity |
    left.dockerfile = right.dockerfile
    and left.target = right.target
    and left.buildArgs = right.buildArgs
    implies left = right
} for 6

check BuildRecordVisibilityRequiresBuildWorkflowIdentity {
  no w: World, p: Playground, b: BuildRecord |
    b in w.visibleBuilds[p] and
    no s: w.services[p] | buildServiceMatchesRecord[p, s, b]
} for 6

check SourceOnlyServicesNeverSeeBuildRecords {
  no w: World, p: Playground, b: BuildRecord |
    b in w.visibleBuilds[p] and
    all s: w.services[p] | s.workflow != BuildWorkflow
} for 6

check BuildSuccessRequiresExactSourceIdentity {
  no w: World, b: BuildRecord |
    w.buildStatus[b] = BuildSuccess and
    (w.remoteCheckout[b] != b.commit or
     w.imageSourceCommit[b] != b.commit or
     w.imageMetadataCommit[b] != b.commit)
} for 6

check StaleBuildsAreOnlyBuilding {
  no w: World, b: BuildRecord |
    b in w.staleBuild and w.buildStatus[b] != BuildBuilding
} for 6

check ProtectedWorkIsNeverAutoSyncedOrRecreated {
  no w: World, p: Playground |
    p in w.protectedLocalWork and (p in w.sourceSynced or p in w.needsRecreation)
} for 6

check AuthRequiredServicesHaveEffectiveAuth {
  no s: Service |
    s.authRequired = AuthRequired and s.auth = AuthAbsent
} for 6

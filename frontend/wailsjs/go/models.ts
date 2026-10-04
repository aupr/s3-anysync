export namespace api {
	
	export class ActionStatus {
	    action: model.Action;
	    profileId: string;
	    state: string;
	    running: boolean;
	    // Go type: time
	    lastRun: any;
	    lastOutcome?: string;
	    lastSummary?: string;
	    lastError?: string;
	    failures: number;
	    // Go type: time
	    nextRun: any;
	    conflicts: number;
	    pendingDeletes: number;
	
	    static createFrom(source: any = {}) {
	        return new ActionStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = this.convertValues(source["action"], model.Action);
	        this.profileId = source["profileId"];
	        this.state = source["state"];
	        this.running = source["running"];
	        this.lastRun = this.convertValues(source["lastRun"], null);
	        this.lastOutcome = source["lastOutcome"];
	        this.lastSummary = source["lastSummary"];
	        this.lastError = source["lastError"];
	        this.failures = source["failures"];
	        this.nextRun = this.convertValues(source["nextRun"], null);
	        this.conflicts = source["conflicts"];
	        this.pendingDeletes = source["pendingDeletes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AddItemRequest {
	    item: model.Item;
	    localPath: string;
	
	    static createFrom(source: any = {}) {
	        return new AddItemRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.item = this.convertValues(source["item"], model.Item);
	        this.localPath = source["localPath"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AddProfileRequest {
	    profile: model.Profile;
	    secret: string;
	
	    static createFrom(source: any = {}) {
	        return new AddProfileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profile = this.convertValues(source["profile"], model.Profile);
	        this.secret = source["secret"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CreateActionRequest {
	    profileId: string;
	    action: model.Action;
	    passphrase?: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateActionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.action = this.convertValues(source["action"], model.Action);
	        this.passphrase = source["passphrase"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class JoinActionRequest {
	    profileId: string;
	    actionId: string;
	    passphrase?: string;
	
	    static createFrom(source: any = {}) {
	        return new JoinActionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.actionId = source["actionId"];
	        this.passphrase = source["passphrase"];
	    }
	}
	export class RestoreRequest {
	    ts: string;
	    paths?: string[];
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new RestoreRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ts = source["ts"];
	        this.paths = source["paths"];
	        this.to = source["to"];
	    }
	}
	export class Settings {
	    machineId: string;
	    machineName: string;
	    autostart: boolean;
	    autostartSupported: boolean;
	    logLevel: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.machineId = source["machineId"];
	        this.machineName = source["machineName"];
	        this.autostart = source["autostart"];
	        this.autostartSupported = source["autostartSupported"];
	        this.logLevel = source["logLevel"];
	    }
	}
	export class Status {
	    machineId: string;
	    machineName: string;
	    version: string;
	    agent: boolean;
	    actions: ActionStatus[];
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.machineId = source["machineId"];
	        this.machineName = source["machineName"];
	        this.version = source["version"];
	        this.agent = source["agent"];
	        this.actions = this.convertValues(source["actions"], ActionStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateProfileRequest {
	    profile: model.Profile;
	    secret?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateProfileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profile = this.convertValues(source["profile"], model.Profile);
	        this.secret = source["secret"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateRulesRequest {
	    rules: model.Rules;
	    // Go type: time
	    expectedUpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new UpdateRulesRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rules = this.convertValues(source["rules"], model.Rules);
	        this.expectedUpdatedAt = this.convertValues(source["expectedUpdatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace engine {
	
	export class ConflictDetail {
	    conflict: model.Conflict;
	    localText: string;
	    remoteText: string;
	    binary: boolean;
	    tooLarge: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConflictDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conflict = this.convertValues(source["conflict"], model.Conflict);
	        this.localText = source["localText"];
	        this.remoteText = source["remoteText"];
	        this.binary = source["binary"];
	        this.tooLarge = source["tooLarge"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RestoreResult {
	    restored: string[];
	    skippedUnmapped: string[];
	
	    static createFrom(source: any = {}) {
	        return new RestoreResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.restored = source["restored"];
	        this.skippedUnmapped = source["skippedUnmapped"];
	    }
	}

}

export namespace gui {
	
	export class ConnState {
	    connected: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.reason = source["reason"];
	    }
	}

}

export namespace model {
	
	export class Item {
	    id: string;
	    remoteName: string;
	    type: string;
	    excludes: string[];
	    localPaths: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.remoteName = source["remoteName"];
	        this.type = source["type"];
	        this.excludes = source["excludes"];
	        this.localPaths = source["localPaths"];
	    }
	}
	export class Schedule {
	    kind: string;
	    everyMinutes?: number;
	    at?: string;
	    weekday?: number;
	    cron?: string;
	
	    static createFrom(source: any = {}) {
	        return new Schedule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.everyMinutes = source["everyMinutes"];
	        this.at = source["at"];
	        this.weekday = source["weekday"];
	        this.cron = source["cron"];
	    }
	}
	export class Triggers {
	    onChange: boolean;
	    debounceSeconds: number;
	    schedule: Schedule;
	    remotePollMinutes: number;
	
	    static createFrom(source: any = {}) {
	        return new Triggers(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.onChange = source["onChange"];
	        this.debounceSeconds = source["debounceSeconds"];
	        this.schedule = this.convertValues(source["schedule"], Schedule);
	        this.remotePollMinutes = source["remotePollMinutes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Action {
	    id: string;
	    name: string;
	    direction: string;
	    machineDirections: Record<string, string>;
	    triggers: Triggers;
	    propagateDeletes: boolean;
	    receiverRestoresCloud: boolean;
	    encrypt: boolean;
	    excludes: string[];
	    snapshotRetention: number;
	    deleteGuardMinFiles: number;
	    deleteGuardPercent: number;
	    items: Item[];
	    machines: Record<string, string>;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.direction = source["direction"];
	        this.machineDirections = source["machineDirections"];
	        this.triggers = this.convertValues(source["triggers"], Triggers);
	        this.propagateDeletes = source["propagateDeletes"];
	        this.receiverRestoresCloud = source["receiverRestoresCloud"];
	        this.encrypt = source["encrypt"];
	        this.excludes = source["excludes"];
	        this.snapshotRetention = source["snapshotRetention"];
	        this.deleteGuardMinFiles = source["deleteGuardMinFiles"];
	        this.deleteGuardPercent = source["deleteGuardPercent"];
	        this.items = this.convertValues(source["items"], Item);
	        this.machines = source["machines"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileEntry {
	    path: string;
	    hash: string;
	    size: number;
	    mtime: number;
	    etag?: string;
	    writer?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.hash = source["hash"];
	        this.size = source["size"];
	        this.mtime = source["mtime"];
	        this.etag = source["etag"];
	        this.writer = source["writer"];
	    }
	}
	export class Conflict {
	    path: string;
	    local?: FileEntry;
	    remote?: FileEntry;
	    // Go type: time
	    detectedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Conflict(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.local = this.convertValues(source["local"], FileEntry);
	        this.remote = this.convertValues(source["remote"], FileEntry);
	        this.detectedAt = this.convertValues(source["detectedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class HistoryEntry {
	    id: number;
	    actionId: string;
	    kind: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt: any;
	    outcome: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.actionId = source["actionId"];
	        this.kind = source["kind"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.outcome = source["outcome"];
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class PendingDeletes {
	    paths: string[];
	    approved: boolean;
	    // Go type: time
	    detectedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new PendingDeletes(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.approved = source["approved"];
	        this.detectedAt = this.convertValues(source["detectedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Profile {
	    id: string;
	    name: string;
	    provider: string;
	    endpoint: string;
	    region: string;
	    bucket: string;
	    rootPrefix: string;
	    accessKeyId: string;
	    pathStyle: boolean;
	    conditionalWrites: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.endpoint = source["endpoint"];
	        this.region = source["region"];
	        this.bucket = source["bucket"];
	        this.rootPrefix = source["rootPrefix"];
	        this.accessKeyId = source["accessKeyId"];
	        this.pathStyle = source["pathStyle"];
	        this.conditionalWrites = source["conditionalWrites"];
	    }
	}
	export class Rules {
	    name: string;
	    direction: string;
	    triggers: Triggers;
	    propagateDeletes: boolean;
	    receiverRestoresCloud: boolean;
	    excludes: string[];
	    snapshotRetention: number;
	    deleteGuardMinFiles: number;
	    deleteGuardPercent: number;
	
	    static createFrom(source: any = {}) {
	        return new Rules(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.direction = source["direction"];
	        this.triggers = this.convertValues(source["triggers"], Triggers);
	        this.propagateDeletes = source["propagateDeletes"];
	        this.receiverRestoresCloud = source["receiverRestoresCloud"];
	        this.excludes = source["excludes"];
	        this.snapshotRetention = source["snapshotRetention"];
	        this.deleteGuardMinFiles = source["deleteGuardMinFiles"];
	        this.deleteGuardPercent = source["deleteGuardPercent"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class SnapshotInfo {
	    ts: string;
	    reason: string;
	    machineId: string;
	    // Go type: time
	    createdAt: any;
	    files: FileEntry[];
	    deletes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new SnapshotInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ts = source["ts"];
	        this.reason = source["reason"];
	        this.machineId = source["machineId"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.files = this.convertValues(source["files"], FileEntry);
	        this.deletes = source["deletes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncResult {
	    outcome: string;
	    uploaded: number;
	    downloaded: number;
	    deletedRemote: number;
	    deletedLocal: number;
	    adopted: number;
	    conflicts: Conflict[];
	    deferred: string[];
	    pendingDeletes: string[];
	    unsupported: string[];
	
	    static createFrom(source: any = {}) {
	        return new SyncResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.outcome = source["outcome"];
	        this.uploaded = source["uploaded"];
	        this.downloaded = source["downloaded"];
	        this.deletedRemote = source["deletedRemote"];
	        this.deletedLocal = source["deletedLocal"];
	        this.adopted = source["adopted"];
	        this.conflicts = this.convertValues(source["conflicts"], Conflict);
	        this.deferred = source["deferred"];
	        this.pendingDeletes = source["pendingDeletes"];
	        this.unsupported = source["unsupported"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace storage {
	
	export class ProbeResult {
	    list: boolean;
	    put: boolean;
	    get: boolean;
	    delete: boolean;
	    conditionalWrites: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProbeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = source["list"];
	        this.put = source["put"];
	        this.get = source["get"];
	        this.delete = source["delete"];
	        this.conditionalWrites = source["conditionalWrites"];
	    }
	}

}


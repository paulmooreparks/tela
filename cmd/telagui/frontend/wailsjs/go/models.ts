export namespace main {
	
	export class AgentInfo {
	    id: string;
	    agentId?: string;
	    hubId?: string;
	    machineRegistrationId?: string;
	    machineName?: string;
	    hub: string;
	    online: boolean;
	    version: string;
	    hostname: string;
	    os: string;
	    displayName: string;
	    tags: string[];
	    location: string;
	    owner: string;
	    sessionCount: number;
	    registeredAt: string;
	    lastSeen: string;
	    services: any[];
	    capabilities: Record<string, any>;
	    linkedAgentIds?: string[];
	    sources?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.agentId = source["agentId"];
	        this.hubId = source["hubId"];
	        this.machineRegistrationId = source["machineRegistrationId"];
	        this.machineName = source["machineName"];
	        this.hub = source["hub"];
	        this.online = source["online"];
	        this.version = source["version"];
	        this.hostname = source["hostname"];
	        this.os = source["os"];
	        this.displayName = source["displayName"];
	        this.tags = source["tags"];
	        this.location = source["location"];
	        this.owner = source["owner"];
	        this.sessionCount = source["sessionCount"];
	        this.registeredAt = source["registeredAt"];
	        this.lastSeen = source["lastSeen"];
	        this.services = source["services"];
	        this.capabilities = source["capabilities"];
	        this.linkedAgentIds = source["linkedAgentIds"];
	        this.sources = source["sources"];
	    }
	}
	export class BinaryInfo {
	    name: string;
	    path: string;
	    found: boolean;
	    version: string;
	    latest: string;
	    upToDate: boolean;
	    serviceInstalled: boolean;
	    serviceRunning: boolean;
	    servicePath: string;
	    serviceVersion: string;
	
	    static createFrom(source: any = {}) {
	        return new BinaryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.found = source["found"];
	        this.version = source["version"];
	        this.latest = source["latest"];
	        this.upToDate = source["upToDate"];
	        this.serviceInstalled = source["serviceInstalled"];
	        this.serviceRunning = source["serviceRunning"];
	        this.servicePath = source["servicePath"];
	        this.serviceVersion = source["serviceVersion"];
	    }
	}
	export class CommandLogEntry {
	    time: string;
	    method: string;
	    description: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandLogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.method = source["method"];
	        this.description = source["description"];
	        this.command = source["command"];
	    }
	}
	export class ProfileService {
	    name: string;
	    local: number;
	    remote?: number;
	
	    static createFrom(source: any = {}) {
	        return new ProfileService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.local = source["local"];
	        this.remote = source["remote"];
	    }
	}
	export class ProfileConnection {
	    hubId?: string;
	    hub: string;
	    agentId?: string;
	    machine: string;
	    token?: string;
	    services: ProfileService[];
	
	    static createFrom(source: any = {}) {
	        return new ProfileConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hubId = source["hubId"];
	        this.hub = source["hub"];
	        this.agentId = source["agentId"];
	        this.machine = source["machine"];
	        this.token = source["token"];
	        this.services = this.convertValues(source["services"], ProfileService);
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
	export class ConnectionState {
	    connected: boolean;
	    attached: boolean;
	    pid: number;
	    output: string;
	    connections: ProfileConnection[];
	
	    static createFrom(source: any = {}) {
	        return new ConnectionState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.attached = source["attached"];
	        this.pid = source["pid"];
	        this.output = source["output"];
	        this.connections = this.convertValues(source["connections"], ProfileConnection);
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
	export class CredentialInfo {
	    hubUrl: string;
	    identity: string;
	
	    static createFrom(source: any = {}) {
	        return new CredentialInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hubUrl = source["hubUrl"];
	        this.identity = source["identity"];
	    }
	}
	export class ServiceInfo {
	    name: string;
	    port: number;
	    proto: string;
	
	    static createFrom(source: any = {}) {
	        return new ServiceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.proto = source["proto"];
	    }
	}
	export class MachineStatus {
	    id: string;
	    hostname: string;
	    os: string;
	    agentConnected: boolean;
	    sessionCount: number;
	    lastSeen: string;
	    services: ServiceInfo[];
	    capabilities?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new MachineStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.hostname = source["hostname"];
	        this.os = source["os"];
	        this.agentConnected = source["agentConnected"];
	        this.sessionCount = source["sessionCount"];
	        this.lastSeen = source["lastSeen"];
	        this.services = this.convertValues(source["services"], ServiceInfo);
	        this.capabilities = source["capabilities"];
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
	export class HubStatus {
	    online: boolean;
	    hubName: string;
	    machines: MachineStatus[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new HubStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.online = source["online"];
	        this.hubName = source["hubName"];
	        this.machines = this.convertValues(source["machines"], MachineStatus);
	        this.error = source["error"];
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
	export class KnownHub {
	    name: string;
	    url: string;
	    hasToken: boolean;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new KnownHub(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.hasToken = source["hasToken"];
	        this.source = source["source"];
	    }
	}
	export class LocalInstance {
	    binary: string;
	    pid: number;
	    machines?: string[];
	    hub?: string;
	    configPath?: string;
	    port?: number;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new LocalInstance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.binary = source["binary"];
	        this.pid = source["pid"];
	        this.machines = source["machines"];
	        this.hub = source["hub"];
	        this.configPath = source["configPath"];
	        this.port = source["port"];
	        this.name = source["name"];
	    }
	}
	export class LogTabInfo {
	    type: string;
	    id: string;
	    hub: string;
	
	    static createFrom(source: any = {}) {
	        return new LogTabInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.id = source["id"];
	        this.hub = source["hub"];
	    }
	}
	
	export class PortAssignment {
	    key: string;
	    localPort: number;
	
	    static createFrom(source: any = {}) {
	        return new PortAssignment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.localPort = source["localPort"];
	    }
	}
	export class PortalDeviceAuthStartResult {
	    baseURL: string;
	    deviceCode: string;
	    userCode: string;
	    verificationURI: string;
	    expiresIn: number;
	    interval: number;
	
	    static createFrom(source: any = {}) {
	        return new PortalDeviceAuthStartResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseURL = source["baseURL"];
	        this.deviceCode = source["deviceCode"];
	        this.userCode = source["userCode"];
	        this.verificationURI = source["verificationURI"];
	        this.expiresIn = source["expiresIn"];
	        this.interval = source["interval"];
	    }
	}
	export class PortalSource {
	    name: string;
	    kind: string;
	    url: string;
	    enabled: boolean;
	    addedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new PortalSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.url = source["url"];
	        this.enabled = source["enabled"];
	        this.addedAt = source["addedAt"];
	    }
	}
	
	export class ProfileMount {
	    mount: string;
	    port: number;
	    auto: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProfileMount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mount = source["mount"];
	        this.port = source["port"];
	        this.auto = source["auto"];
	    }
	}
	
	export class RemoteInfo {
	    name: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new RemoteInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	    }
	}
	
	export class Settings {
	    autoConnect: boolean;
	    reconnectOnDrop: boolean;
	    minimizeTo: string;
	    startMinimized: boolean;
	    minimizeOnClose: boolean;
	    autoCheckUpdates: boolean;
	    verboseDefault: boolean;
	    confirmDisconnect: boolean;
	    sidebarWidth: number;
	    hubsSidebarWidth: number;
	    defaultProfile: string;
	    binPath: string;
	    connectTooltipDismissed: boolean;
	    theme: string;
	    hideDotfiles?: boolean;
	    logPanelHeight: number;
	    logPanelCollapsed: boolean;
	    logMaxLines: number;
	    windowSaved: boolean;
	    windowX: number;
	    windowY: number;
	    windowWidth: number;
	    windowHeight: number;
	    openLogTabs: LogTabInfo[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.autoConnect = source["autoConnect"];
	        this.reconnectOnDrop = source["reconnectOnDrop"];
	        this.minimizeTo = source["minimizeTo"];
	        this.startMinimized = source["startMinimized"];
	        this.minimizeOnClose = source["minimizeOnClose"];
	        this.autoCheckUpdates = source["autoCheckUpdates"];
	        this.verboseDefault = source["verboseDefault"];
	        this.confirmDisconnect = source["confirmDisconnect"];
	        this.sidebarWidth = source["sidebarWidth"];
	        this.hubsSidebarWidth = source["hubsSidebarWidth"];
	        this.defaultProfile = source["defaultProfile"];
	        this.binPath = source["binPath"];
	        this.connectTooltipDismissed = source["connectTooltipDismissed"];
	        this.theme = source["theme"];
	        this.hideDotfiles = source["hideDotfiles"];
	        this.logPanelHeight = source["logPanelHeight"];
	        this.logPanelCollapsed = source["logPanelCollapsed"];
	        this.logMaxLines = source["logMaxLines"];
	        this.windowSaved = source["windowSaved"];
	        this.windowX = source["windowX"];
	        this.windowY = source["windowY"];
	        this.windowWidth = source["windowWidth"];
	        this.windowHeight = source["windowHeight"];
	        this.openLogTabs = this.convertValues(source["openLogTabs"], LogTabInfo);
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
	export class ToolVersions {
	    gui: string;
	    cli: string;
	    latest: string;
	    guiBehind: boolean;
	    cliBehind: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolVersions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.gui = source["gui"];
	        this.cli = source["cli"];
	        this.latest = source["latest"];
	        this.guiBehind = source["guiBehind"];
	        this.cliBehind = source["cliBehind"];
	    }
	}
	export class UpdateInfo {
	    pending: boolean;
	    version: string;
	    gui: string;
	    cli: string;
	    guiBehind: boolean;
	    cliBehind: boolean;
	    packageManaged: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pending = source["pending"];
	        this.version = source["version"];
	        this.gui = source["gui"];
	        this.cli = source["cli"];
	        this.guiBehind = source["guiBehind"];
	        this.cliBehind = source["cliBehind"];
	        this.packageManaged = source["packageManaged"];
	    }
	}

}


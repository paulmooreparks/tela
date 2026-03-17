export namespace main {
	
	export class CommandLogEntry {
	    time: string;
	    description: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandLogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.description = source["description"];
	        this.command = source["command"];
	    }
	}
	export class ProfileService {
	    name: string;
	    local: number;
	
	    static createFrom(source: any = {}) {
	        return new ProfileService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.local = source["local"];
	    }
	}
	export class ProfileConnection {
	    hub: string;
	    machine: string;
	    token?: string;
	    services: ProfileService[];
	
	    static createFrom(source: any = {}) {
	        return new ProfileConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hub = source["hub"];
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
	    pid: number;
	    output: string;
	    connections: ProfileConnection[];
	
	    static createFrom(source: any = {}) {
	        return new ConnectionState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
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
	
	

}


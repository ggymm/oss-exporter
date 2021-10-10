APIData.prototype.toString = function () {
    return this.str
}

APIStatus.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"status",' + this.props + ' }'
}

APISystem.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"system",' + this.props + '"redundancies":[' + this.redundancies + ']}'
}

APIRedundancy.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"redundancy",' + this.props + ' }'
}

APIHostGroup.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"host_group",' + this.props + '"hosts":[' + this.hosts + ']}'
}

APIHost.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"host",' + this.props + '"initiator":[' + this.initiators + ']}'
}

APIInitiator.prototype.toString = function () {
    return '{"oid":"' + this.oid + '","class":"initiator",' + this.props + ' }'
}

APIProp.prototype.toString = function () {
    return '"' + this.name + '":"' + this.value + '"'
}

function ObjectToJson(apiData) {
    return apiData.toString()
}

function APIData(C) {
    if (C instanceof Array) {
        this.str = "["
        for (var i = 0; i < C.length; i++) {
            if (i === C.length - 1) {
                this.str += C[i].toString()
            } else {
                this.str += C[i].toString() + ","
            }
        }
        this.str += "]"
    } else {
        return "{}"
    }
}

function APIStatus(C, A) {
    this.props = ""
    for (var i = 0; i < C.length; i++) {
        if (i === C.length - 1) {
            this.props += C[i].toString()
        } else {
            this.props += C[i].toString() + ","
        }
    }
    this.oid = A.oid
}

function APISystem(C, A) {
    this.props = ""
    this.redundancies = ""
    for (var i = 0; i < C.length; i++) {
        if (C[i] instanceof APIProp) {
            if (i === C.length - 1) {
                this.props += C[i].toString()
            } else {
                this.props += C[i].toString() + ","
            }
        } else if (C[i] instanceof APIRedundancy) {
            if (i === C.length - 1) {
                this.redundancies += C[i].toString()
            } else {
                this.redundancies += C[i].toString() + ","
            }
        }
    }
    this.oid = A.oid
}

function APIRedundancy(C, A) {
    this.props = ""
    for (var i = 0; i < C.length; i++) {
        if (i === C.length - 1) {
            this.props += C[i].toString()
        } else {
            this.props += C[i].toString() + ","
        }
    }
    this.oid = A.oid
}

function APIHostGroup(C, A) {
    this.props = ""
    this.hosts = ""
    for (var i = 0; i < C.length; i++) {
        if (C[i] instanceof APIProp) {
            if (i === C.length - 1) {
                this.props += C[i].toString()
            } else {
                this.props += C[i].toString() + ","
            }
        } else if (C[i] instanceof APIHost) {
            if (i === C.length - 1) {
                this.hosts += C[i].toString()
            } else {
                this.hosts += C[i].toString() + ","
            }
        }
    }
    this.oid = A.oid
}

function APIHost(C, A) {
    this.props = ""
    this.initiators = ""
    for (var i = 0; i < C.length; i++) {
        if (C[i] instanceof APIProp) {
            if (i === C.length - 1) {
                this.props += C[i].toString()
            } else {
                this.props += C[i].toString() + ","
            }
        } else if (C[i] instanceof APIInitiator) {
            if (i === C.length - 1) {
                this.initiators += C[i].toString()
            } else {
                this.initiators += C[i].toString() + ","
            }
        }
    }
    if (this.initiators.length === 0) {
        this.props += ","
    }
    this.oid = A.oid
}

function APIInitiator(C, A) {
    this.props = ""
    for (var i = 0; i < C.length; i++) {
        if (i === C.length - 1) {
            this.props += C[i].toString()
        } else {
            this.props += C[i].toString() + ","
        }
    }
    this.oid = A.oid
}

function APIProp(E) {
    this.name = E.name
    this.value = E.value
}
